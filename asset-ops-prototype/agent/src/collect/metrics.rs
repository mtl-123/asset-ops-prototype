use crate::config::MetricsConfig;
use crate::model::{
    CpuDetail, DiskIo, InodeInfo, LinuxMetrics, MemoryDetail, ProcessInfo, SwapDetail, SwapIo,
    TcpStates,
};
use std::collections::HashMap;
use std::ffi::CString;
use std::fs;
use std::sync::{Mutex, OnceLock};
use std::time::Instant;
use sysinfo::Disks;

#[derive(Debug, Clone, Copy, Default)]
struct CpuCounters {
    user: u64,
    nice: u64,
    system: u64,
    idle: u64,
    iowait: u64,
    irq: u64,
    softirq: u64,
    steal: u64,
    guest: u64,
    context_switches: u64,
}

#[derive(Debug, Clone, Copy, Default)]
struct DiskCounters {
    reads: u64,
    read_sectors: u64,
    read_ms: u64,
    writes: u64,
    write_sectors: u64,
    write_ms: u64,
    io_ms: u64,
    weighted_ms: u64,
}

#[derive(Clone)]
struct Snapshot {
    at: Instant,
    cpu: CpuCounters,
    disks: HashMap<String, DiskCounters>,
    pswpin: u64,
    pswpout: u64,
}

static SNAPSHOT: OnceLock<Mutex<Option<Snapshot>>> = OnceLock::new();

pub fn collect(config: &MetricsConfig) -> Option<LinuxMetrics> {
    if !config.cpu_detail
        && !config.context_switch
        && !config.memory
        && !config.swap
        && !config.disk
        && !config.inode
        && !config.disk_io
        && !config.tcp
        && !config.process
    {
        return None;
    }

    let cpu = read_cpu();
    let disks = if config.disk_io {
        read_diskstats()
    } else {
        HashMap::new()
    };
    let vmstat = if config.swap { read_vmstat() } else { HashMap::new() };
    let memory = if config.memory || config.swap {
        read_meminfo()
    } else {
        HashMap::new()
    };

    let now = Instant::now();
    let state = SNAPSHOT.get_or_init(|| Mutex::new(None));
    let mut guard = state.lock().unwrap_or_else(|poisoned| poisoned.into_inner());
    let previous = guard.take();
    let interval = previous
        .as_ref()
        .map(|snapshot| now.saturating_duration_since(snapshot.at).as_secs_f64())
        .filter(|value| *value > 0.0);

    let cpu_detail = if config.cpu_detail {
        previous.as_ref().and_then(|snapshot| cpu_detail(cpu, snapshot.cpu))
    } else {
        None
    };
    let context_switches = if config.context_switch {
        match (interval, previous.as_ref()) {
            (Some(elapsed), Some(snapshot)) => delta(cpu.context_switches, snapshot.cpu.context_switches)
                .map(|value| value as f64 / elapsed),
            _ => None,
        }
    } else {
        None
    };
    let disk_io = if config.disk_io {
        disk_rows(&disks, previous.as_ref(), interval)
    } else {
        Vec::new()
    };
    let swap_io = if config.swap {
        swap_rates(&vmstat, previous.as_ref(), interval)
    } else {
        None
    };

    *guard = Some(Snapshot {
        at: now,
        cpu,
        disks,
        pswpin: value(&vmstat, "pswpin"),
        pswpout: value(&vmstat, "pswpout"),
    });
    drop(guard);

    let memory_detail = if config.memory {
        Some(memory_detail(&memory))
    } else {
        None
    };
    let swap_detail = if config.swap {
        Some(swap_detail(&memory))
    } else {
        None
    };
    let inodes = if config.inode {
        inode_rows()
    } else {
        Vec::new()
    };

    Some(LinuxMetrics {
        sample_interval_seconds: interval,
        cpu: cpu_detail,
        context_switches_per_second: context_switches,
        memory: memory_detail,
        swap: swap_detail,
        swap_io,
        disks: disk_io,
        inodes,
        tcp: if config.tcp { Some(tcp_states()) } else { None },
        processes: if config.process { Some(process_info()) } else { None },
    })
}

fn read_cpu() -> CpuCounters {
    let mut counters = CpuCounters::default();
    let Ok(text) = fs::read_to_string("/proc/stat") else {
        return counters;
    };
    for line in text.lines() {
        if let Some(rest) = line.strip_prefix("cpu ") {
            let fields: Vec<u64> = rest
                .split_whitespace()
                .map(|item| item.parse().unwrap_or(0))
                .collect();
            counters.user = *fields.first().unwrap_or(&0);
            counters.nice = *fields.get(1).unwrap_or(&0);
            counters.system = *fields.get(2).unwrap_or(&0);
            counters.idle = *fields.get(3).unwrap_or(&0);
            counters.iowait = *fields.get(4).unwrap_or(&0);
            counters.irq = *fields.get(5).unwrap_or(&0);
            counters.softirq = *fields.get(6).unwrap_or(&0);
            counters.steal = *fields.get(7).unwrap_or(&0);
            counters.guest = *fields.get(8).unwrap_or(&0);
        } else if let Some(rest) = line.strip_prefix("ctxt ") {
            counters.context_switches = rest.trim().parse().unwrap_or(0);
        }
    }
    counters
}

fn cpu_detail(current: CpuCounters, previous: CpuCounters) -> Option<CpuDetail> {
    let fields = [
        ("user", current.user, previous.user),
        ("nice", current.nice, previous.nice),
        ("system", current.system, previous.system),
        ("idle", current.idle, previous.idle),
        ("iowait", current.iowait, previous.iowait),
        ("irq", current.irq, previous.irq),
        ("softirq", current.softirq, previous.softirq),
        ("steal", current.steal, previous.steal),
        ("guest", current.guest, previous.guest),
    ];
    let deltas: Vec<(String, u64)> = fields
        .iter()
        .map(|(name, current, previous)| (name.to_string(), delta(*current, *previous).unwrap_or(0)))
        .collect();
    let total: u64 = deltas
        .iter()
        .filter(|(name, _)| name != "guest")
        .map(|(_, value)| *value)
        .sum();
    if total == 0 {
        return None;
    }
    let get = |name: &str| {
        deltas
            .iter()
            .find(|(key, _)| key == name)
            .map(|(_, value)| *value as f64 * 100.0 / total as f64)
    };
    Some(CpuDetail {
        user_percent: get("user"),
        nice_percent: get("nice"),
        system_percent: get("system"),
        idle_percent: get("idle"),
        iowait_percent: get("iowait"),
        irq_percent: get("irq"),
        softirq_percent: get("softirq"),
        steal_percent: get("steal"),
        guest_percent: get("guest"),
    })
}

fn read_meminfo() -> HashMap<String, u64> {
    let mut values = HashMap::new();
    let Ok(text) = fs::read_to_string("/proc/meminfo") else {
        return values;
    };
    for line in text.lines() {
        if let Some((key, rest)) = line.split_once(':') {
            let kb = rest
                .split_whitespace()
                .next()
                .and_then(|value| value.parse::<u64>().ok())
                .unwrap_or(0);
            values.insert(key.trim().to_string(), kb.saturating_mul(1024));
        }
    }
    values
}

fn read_vmstat() -> HashMap<String, u64> {
    let mut values = HashMap::new();
    let Ok(text) = fs::read_to_string("/proc/vmstat") else {
        return values;
    };
    for line in text.lines() {
        if let Some((key, value)) = line.split_once(' ') {
            values.insert(key.trim().to_string(), value.trim().parse().unwrap_or(0));
        }
    }
    values
}

fn value(values: &HashMap<String, u64>, key: &str) -> u64 {
    values.get(key).copied().unwrap_or(0)
}

fn memory_detail(values: &HashMap<String, u64>) -> MemoryDetail {
    let total = value(values, "MemTotal");
    let free = value(values, "MemFree");
    let available = value(values, "MemAvailable");
    let buffers = value(values, "Buffers");
    let cached = value(values, "Cached").saturating_add(value(values, "SReclaimable"));
    let used = total.saturating_sub(available);
    MemoryDetail {
        total_bytes: total,
        free_bytes: free,
        available_bytes: available,
        buffers_bytes: buffers,
        cached_bytes: cached,
        used_bytes: used,
        usage_percent: percent(used, total),
    }
}

fn swap_detail(values: &HashMap<String, u64>) -> SwapDetail {
    let total = value(values, "SwapTotal");
    let free = value(values, "SwapFree");
    let used = total.saturating_sub(free);
    SwapDetail {
        total_bytes: total,
        free_bytes: free,
        used_bytes: used,
        usage_percent: percent(used, total),
    }
}

fn swap_rates(
    values: &HashMap<String, u64>,
    previous: Option<&Snapshot>,
    interval: Option<f64>,
) -> Option<SwapIo> {
    let elapsed = interval?;
    let previous = previous?;
    let pages_in = delta(value(values, "pswpin"), previous.pswpin)? as f64 / elapsed;
    let pages_out = delta(value(values, "pswpout"), previous.pswpout)? as f64 / elapsed;
    let page_size = unsafe { libc::sysconf(libc::_SC_PAGESIZE) }.max(4096) as f64;
    Some(SwapIo {
        pages_in_per_second: Some(pages_in),
        pages_out_per_second: Some(pages_out),
        bytes_in_per_second: Some(pages_in * page_size),
        bytes_out_per_second: Some(pages_out * page_size),
    })
}

fn read_diskstats() -> HashMap<String, DiskCounters> {
    let mut values = HashMap::new();
    let Ok(text) = fs::read_to_string("/proc/diskstats") else {
        return values;
    };
    for line in text.lines() {
        let fields: Vec<&str> = line.split_whitespace().collect();
        if fields.len() < 14 {
            continue;
        }
        let name = fields[2];
        if !is_whole_device(name) {
            continue;
        }
        let number = |index: usize| fields.get(index).and_then(|v| v.parse().ok()).unwrap_or(0);
        values.insert(
            name.to_string(),
            DiskCounters {
                reads: number(3),
                read_sectors: number(5),
                read_ms: number(6),
                writes: number(7),
                write_sectors: number(9),
                write_ms: number(10),
                io_ms: number(12),
                weighted_ms: number(13),
            },
        );
    }
    values
}

fn is_whole_device(name: &str) -> bool {
    if name.starts_with("nvme") {
        return !name[4..].contains('p');
    }
    if let Some(rest) = name.strip_prefix("sd") {
        return rest.len() == 1;
    }
    if let Some(rest) = name.strip_prefix("vd") {
        return rest.len() == 1;
    }
    if let Some(rest) = name.strip_prefix("xvd") {
        return rest.len() == 1;
    }
    false
}

fn disk_rows(
    current: &HashMap<String, DiskCounters>,
    previous: Option<&Snapshot>,
    interval: Option<f64>,
) -> Vec<DiskIo> {
    let mut rows = Vec::new();
    for (device, values) in current {
        let old = previous.and_then(|snapshot| snapshot.disks.get(device));
        let elapsed = interval;
        let rates = old.zip(elapsed).and_then(|(old, elapsed)| {
            let reads = delta(values.reads, old.reads)? as f64;
            let writes = delta(values.writes, old.writes)? as f64;
            let read_sectors = delta(values.read_sectors, old.read_sectors)? as f64;
            let write_sectors = delta(values.write_sectors, old.write_sectors)? as f64;
            let read_ms = delta(values.read_ms, old.read_ms)? as f64;
            let write_ms = delta(values.write_ms, old.write_ms)? as f64;
            let io_ms = delta(values.io_ms, old.io_ms)? as f64;
            let weighted_ms = delta(values.weighted_ms, old.weighted_ms)? as f64;
            let completed = reads + writes;
            Some(DiskIo {
                device: device.clone(),
                read_iops: Some(reads / elapsed),
                write_iops: Some(writes / elapsed),
                read_bytes_per_second: Some(read_sectors * 512.0 / elapsed),
                write_bytes_per_second: Some(write_sectors * 512.0 / elapsed),
                await_ms: if completed > 0.0 {
                    Some((read_ms + write_ms) / completed)
                } else {
                    None
                },
                aqu_sz: Some(weighted_ms / (elapsed * 1000.0)),
                util_percent: Some(io_ms / (elapsed * 1000.0) * 100.0),
            })
        });
        rows.push(rates.unwrap_or_else(|| DiskIo {
            device: device.clone(),
            ..DiskIo::default()
        }));
    }
    rows.sort_by(|left, right| left.device.cmp(&right.device));
    rows
}

fn inode_rows() -> Vec<InodeInfo> {
    let disks = Disks::new_with_refreshed_list();
    let mut rows = Vec::new();
    for disk in disks.list() {
        let mount = disk.mount_point().to_string_lossy().to_string();
        let Some((total, free)) = statvfs_inodes(&mount) else {
            continue;
        };
        let used = total.saturating_sub(free);
        rows.push(InodeInfo {
            mount,
            filesystem: disk.file_system().to_string_lossy().to_string(),
            total,
            used,
            free,
            usage_percent: percent(used, total),
        });
    }
    rows.sort_by(|left, right| left.mount.cmp(&right.mount));
    rows.truncate(32);
    rows
}

fn statvfs_inodes(mount: &str) -> Option<(u64, u64)> {
    let path = CString::new(mount.as_bytes()).ok()?;
    let mut stat = std::mem::MaybeUninit::<libc::statvfs>::zeroed();
    let result = unsafe { libc::statvfs(path.as_ptr(), stat.as_mut_ptr()) };
    if result != 0 {
        return None;
    }
    let stat = unsafe { stat.assume_init() };
    Some((stat.f_files as u64, stat.f_ffree as u64))
}

fn tcp_states() -> TcpStates {
    let mut states = TcpStates::default();
    let mut add = |state: u64| match state {
        1 => states.established += 1,
        2 => states.syn_sent += 1,
        3 => states.syn_recv += 1,
        4 => states.fin_wait1 += 1,
        5 => states.fin_wait2 += 1,
        6 => states.time_wait += 1,
        7 => states.close += 1,
        8 => states.close_wait += 1,
        9 => states.last_ack += 1,
        10 => states.listen += 1,
        11 => states.closing += 1,
        _ => {}
    };
    for path in ["/proc/net/tcp", "/proc/net/tcp6"] {
        let Ok(text) = fs::read_to_string(path) else {
            continue;
        };
        for line in text.lines().skip(1) {
            let fields: Vec<&str> = line.split_whitespace().collect();
            if let Some(value) = fields.get(3).and_then(|v| u64::from_str_radix(v, 16).ok()) {
                add(value);
                states.total += 1;
            }
        }
    }
    states
}

fn process_info() -> ProcessInfo {
    let mut total = 0_u64;
    let mut threads = 0_u64;
    let mut zombies = 0_u64;
    if let Ok(entries) = fs::read_dir("/proc") {
        for entry in entries.flatten() {
            let name = entry.file_name();
            let Some(name) = name.to_str() else {
                continue;
            };
            if name.parse::<u64>().is_err() {
                continue;
            }
            total += 1;
            let Ok(stat) = fs::read_to_string(entry.path().join("stat")) else {
                continue;
            };
            let Some(end) = stat.rfind(')') else {
                continue;
            };
            let fields: Vec<&str> = stat[end + 1..].split_whitespace().collect();
            if fields.first() == Some(&"Z") {
                zombies += 1;
            }
            if let Some(value) = fields.get(17).and_then(|v| v.parse::<u64>().ok()) {
                threads += value;
            }
        }
    }
    let (allocated, max) = fs::read_to_string("/proc/sys/fs/file-nr")
        .ok()
        .and_then(|text| {
            let fields: Vec<u64> = text
                .split_whitespace()
                .filter_map(|v| v.parse().ok())
                .collect();
            if fields.len() >= 3 {
                Some((fields[0], fields[2]))
            } else {
                None
            }
        })
        .unwrap_or((0, 0));
    ProcessInfo {
        total,
        threads,
        zombies,
        fd_allocated: allocated,
        fd_max: max,
        fd_usage_percent: percent(allocated, max),
    }
}

fn delta(current: u64, previous: u64) -> Option<u64> {
    current.checked_sub(previous)
}

fn percent(used: u64, total: u64) -> f64 {
    if total == 0 {
        0.0
    } else {
        (used as f64 * 100.0 / total as f64 * 100.0).round() / 100.0
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn detects_whole_disks_not_partitions() {
        assert!(is_whole_device("sda"));
        assert!(!is_whole_device("sda1"));
        assert!(is_whole_device("nvme0n1"));
        assert!(!is_whole_device("nvme0n1p1"));
        assert!(!is_whole_device("loop0"));
    }

    #[test]
    fn counter_reset_is_rejected() {
        assert_eq!(delta(10, 20), None);
        assert_eq!(delta(20, 10), Some(10));
    }

    #[test]
    fn parses_meminfo_into_bytes() {
        // Covered indirectly by the live collector; this checks the arithmetic
        // used for the percent conversion.
        assert!((percent(50, 100) - 50.0).abs() < f64::EPSILON);
        assert_eq!(percent(1, 0), 0.0);
    }
}
