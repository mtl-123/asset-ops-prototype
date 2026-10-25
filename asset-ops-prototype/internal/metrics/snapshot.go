package metrics

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Script is one POSIX sh block that prints a `qiyun:` prefixed key/value stream.
// It is deliberately a single round trip: over a slow link every extra exec costs
// a full RTT, and the page refreshes often.
const Script = `LC_ALL=C LANG=C
echo "qiyun:host=$(hostname 2>/dev/null || uname -n 2>/dev/null)"
echo "qiyun:kernel=$(uname -r 2>/dev/null)"
echo "qiyun:cores=$(nproc 2>/dev/null || getconf _NPROCESSORS_ONLN 2>/dev/null || echo 0)"
echo "qiyun:uptime=$(cut -d. -f1 /proc/uptime 2>/dev/null || echo 0)"
echo "qiyun:load=$(cut -d' ' -f1-3 /proc/loadavg 2>/dev/null)"
echo "qiyun:os=$(sed -n 's/^PRETTY_NAME=//p' /etc/os-release 2>/dev/null | head -n1 | tr -d '"')"
qiyun_cpu() { awk '/^cpu /{t=0; for(i=2;i<=NF;i++) t+=$i; printf "%d %d\n", $5+$6, t}' /proc/stat 2>/dev/null; }
echo "qiyun:cpu1=$(qiyun_cpu)"
sleep 0.3
echo "qiyun:cpu2=$(qiyun_cpu)"
awk '/^MemTotal:/{t=$2} /^MemAvailable:/{a=$2} /^MemFree:/{f=$2} /^SwapTotal:/{s=$2} /^SwapFree:/{sf=$2} END{printf "qiyun:mem=%s %s %s %s %s\n", t, a, f, s, sf}' /proc/meminfo 2>/dev/null
df -P 2>/dev/null | awk 'NR>1 && NF>=6 {dev=$1; if (dev ~ /^(tmpfs|devtmpfs|squashfs|overlay|udev|none)$/) next; mount=$6; if (mount ~ /^\/(run|sys|proc|dev)(\/|$)/) next; cap=$5; gsub("%","",cap); printf "qiyun:fs=%s|%s|%s|%s|%s\n", dev, mount, $2, $3, cap}'
echo "qiyun:end=1"
`

// Snapshot is one parsed sample of a Linux host.
type Snapshot struct {
	Hostname       string    `json:"hostname"`
	OS             string    `json:"os"`
	Kernel         string    `json:"kernel"`
	Cores          int       `json:"cores"`
	UptimeSeconds  int64     `json:"uptime_seconds"`
	Load1          float64   `json:"load1"`
	Load5          float64   `json:"load5"`
	Load15         float64   `json:"load15"`
	CPUPercent     float64   `json:"cpu_percent"`
	MemTotalBytes  int64     `json:"mem_total_bytes"`
	MemUsedBytes   int64     `json:"mem_used_bytes"`
	MemAvailable   int64     `json:"mem_available_bytes"`
	MemPercent     float64   `json:"mem_percent"`
	SwapTotalBytes int64     `json:"swap_total_bytes"`
	SwapUsedBytes  int64     `json:"swap_used_bytes"`
	Disks          []Disk    `json:"disks"`
	DiskPercent    float64   `json:"disk_percent"`
	CollectedAt    time.Time `json:"collected_at"`
}

// Disk is one mounted filesystem. Sizes are bytes; df reports 1 KiB blocks.
type Disk struct {
	Device      string  `json:"device"`
	Mount       string  `json:"mount"`
	TotalBytes  int64   `json:"total_bytes"`
	UsedBytes   int64   `json:"used_bytes"`
	UsedPercent float64 `json:"used_percent"`
}

// ErrUnparseable means the host answered but the payload had nothing usable —
// usually a non-Linux host or a shell that mangled the script.
var ErrUnparseable = errors.New("metrics: 远端未返回可解析的指标")

type cpuPair struct{ idle, total int64 }

// Parse turns the Script output into a Snapshot. It never guesses: a payload
// without the end marker is rejected so a truncated read cannot look healthy.
func Parse(raw string, now time.Time) (Snapshot, error) {
	var (
		snap                 Snapshot
		cpu1, cpu2           cpuPair
		memTotal, memAvail   int64
		memFree, swapTotal   int64
		swapFree             int64
		sawMem, sawFS, ended bool
	)
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimRight(line, "\r")
		if !strings.HasPrefix(line, "qiyun:") {
			continue
		}
		key, value, ok := strings.Cut(strings.TrimPrefix(line, "qiyun:"), "=")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		switch key {
		case "host":
			snap.Hostname = value
		case "kernel":
			snap.Kernel = value
		case "os":
			snap.OS = strings.Trim(value, `"'`)
		case "cores":
			snap.Cores = atoi(value)
		case "uptime":
			snap.UptimeSeconds = int64(atoi(value))
		case "load":
			fields := strings.Fields(value)
			if len(fields) >= 3 {
				snap.Load1 = atof(fields[0])
				snap.Load5 = atof(fields[1])
				snap.Load15 = atof(fields[2])
			}
		case "cpu1":
			cpu1 = parseCPU(value)
		case "cpu2":
			cpu2 = parseCPU(value)
		case "mem":
			fields := strings.Fields(value)
			// /proc/meminfo is in kB; everything else we store is bytes.
			if len(fields) >= 5 {
				memTotal, memAvail, memFree = int64(atoi(fields[0]))*1024, int64(atoi(fields[1]))*1024, int64(atoi(fields[2]))*1024
				swapTotal, swapFree = int64(atoi(fields[3]))*1024, int64(atoi(fields[4]))*1024
				sawMem = true
			}
		case "fs":
			parts := strings.Split(value, "|")
			if len(parts) != 5 {
				continue
			}
			total, used := int64(atoi(parts[2]))*1024, int64(atoi(parts[3]))*1024
			if total <= 0 {
				continue
			}
			disk := Disk{Device: parts[0], Mount: parts[1], TotalBytes: total, UsedBytes: used, UsedPercent: atof(parts[4])}
			if disk.UsedPercent <= 0 && used > 0 {
				disk.UsedPercent = round1(float64(used) / float64(total) * 100)
			}
			snap.Disks = append(snap.Disks, disk)
			sawFS = true
		case "end":
			ended = true
		}
	}
	if !ended || (snap.Hostname == "" && !sawMem && !sawFS) {
		return Snapshot{}, ErrUnparseable
	}
	if cpu2.total > cpu1.total && cpu2.total > 0 {
		idleDelta := float64(cpu2.idle - cpu1.idle)
		totalDelta := float64(cpu2.total - cpu1.total)
		snap.CPUPercent = clampPercent(round1((1 - idleDelta/totalDelta) * 100))
	}
	if memTotal > 0 {
		// MemAvailable is the honest "usable" number; MemFree alone reports cache as used.
		used := memTotal - memAvail
		if memAvail <= 0 {
			used = memTotal - memFree
		}
		if used < 0 {
			used = 0
		}
		if used > memTotal {
			used = memTotal
		}
		snap.MemTotalBytes, snap.MemUsedBytes, snap.MemAvailable = memTotal, used, memAvail
		snap.MemPercent = round1(float64(used) / float64(memTotal) * 100)
	}
	if swapTotal > 0 {
		snap.SwapTotalBytes = swapTotal
		used := swapTotal - swapFree
		if used < 0 {
			used = 0
		}
		snap.SwapUsedBytes = used
	}
	if sawFS {
		snap.DiskPercent = rootDiskPercent(snap.Disks)
	}
	snap.CollectedAt = now.UTC()
	if snap.OS == "" {
		snap.OS = "Linux"
	}
	return snap, nil
}

// rootDiskPercent prefers "/", then the largest mount, so a host with only
// /data still reports something meaningful instead of zero.
func rootDiskPercent(disks []Disk) float64 {
	for _, d := range disks {
		if d.Mount == "/" {
			return d.UsedPercent
		}
	}
	var best Disk
	for _, d := range disks {
		if d.TotalBytes > best.TotalBytes {
			best = d
		}
	}
	return best.UsedPercent
}

func parseCPU(value string) cpuPair {
	fields := strings.Fields(value)
	if len(fields) < 2 {
		return cpuPair{}
	}
	return cpuPair{idle: int64(atoi(fields[0])), total: int64(atoi(fields[1]))}
}

func atoi(value string) int {
	n, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return 0
	}
	return n
}

func atof(value string) float64 {
	f, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
	if err != nil {
		return 0
	}
	return f
}

func clampPercent(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}

func round1(v float64) float64 {
	return float64(int64(v*10+0.5)) / 10
}

// Summary renders a one-line human summary for logs and API errors.
func (s Snapshot) Summary() string {
	return fmt.Sprintf("%s cpu=%.1f%% mem=%.1f%% disk=%.1f%%", s.Hostname, s.CPUPercent, s.MemPercent, s.DiskPercent)
}
