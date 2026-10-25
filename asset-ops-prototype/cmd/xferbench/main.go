// Command xferbench measures how fast this host can push bytes to a managed
// asset, split by transport. It exists to answer one question that a plain
// "transfer is slow" report cannot: is the ceiling the network, the SSH
// channel, or the SFTP protocol layer?
//
//	go run ./cmd/xferbench -asset <id> -size 64
//
// The tool only reads asset credentials from the data directory; it writes
// probe files under -remote-dir and removes them afterwards.
package main

import (
	"context"
	"crypto/rand"
	"flag"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"

	"qiyun-asset-ops/internal/secretbox"
	"qiyun-asset-ops/internal/sshgateway"
	"qiyun-asset-ops/internal/store"
)

func main() {
	dataDir := flag.String("data", "./data", "data directory holding qiyun.db and master.key")
	assetID := flag.String("asset", "", "asset id (default: first verified linux host)")
	sizeMiB := flag.Int("size", 64, "payload size in MiB")
	remoteDir := flag.String("remote-dir", "/tmp", "directory for probe files on the remote host")
	execCmd := flag.String("exec", "", "run this command on the remote host and exit")
	parallel := flag.String("parallel", "1,2,4", "comma separated stream counts for the parallel upload test")
	skip := flag.String("skip", "", "comma separated test letters to skip (A,B,C,D,F,G)")
	flag.Parse()

	ctx := context.Background()
	box, err := secretbox.LoadOrCreate(filepath.Join(*dataDir, "master.key"))
	must(err)
	st, err := store.Open(filepath.Join(*dataDir, "qiyun.db"), box)
	must(err)
	gw, err := sshgateway.New(st, nil, nil)
	must(err)

	id := *assetID
	if id == "" {
		assets, err := st.ListAssets(ctx, "")
		must(err)
		for _, a := range assets {
			if a.HostKeyStatus == "verified" && a.AssetType == "linux_host" {
				id = a.ID
				break
			}
		}
	}
	if id == "" {
		fatal("no verified linux_host asset found")
	}
	asset, err := st.GetAsset(ctx, id)
	must(err)
	if *execCmd != "" {
		client, _, _, err := gw.Dial(ctx, id)
		must(err)
		out, code, err := execOut(client, *execCmd)
		fmt.Print(out)
		if err != nil {
			fmt.Fprintf(os.Stderr, "exit=%d err=%v\n", code, err)
			os.Exit(1)
		}
		return
	}
	fmt.Printf("asset   %s  %s:%d  user=%s\n", asset.Name, asset.Address, asset.SSHPort, asset.Username)

	payload := *sizeMiB << 20
	raw := make([]byte, payload)
	_, _ = rand.Read(raw)
	fmt.Printf("payload %d MiB\n\n", *sizeMiB)

	start := time.Now()
	client, _, _, err := gw.Dial(ctx, id)
	must(err)
	fmt.Printf("dial    %s\n", time.Since(start).Round(time.Millisecond))

	defer func() {
		cl := client
		_ = cl.Close()
	}()

	// A/ B: raw SSH channel. This is what `scp` uses: the bytes go straight into
	// a pipe with no per-request acknowledgements.
	if want(*skip, "A") {
		runRaw(client, raw, path.Join(*remoteDir, "qiyun-bench-raw.bin"), false)
	}
	if want(*skip, "B") {
		runRaw(client, raw, path.Join(*remoteDir, "qiyun-bench-rawfsync.bin"), true)
	}
	// C/D: SFTP with different in-flight windows.
	if want(*skip, "C") {
		runSFTP(client, raw, path.Join(*remoteDir, "qiyun-bench-sftp.bin"), 64)
	}
	if want(*skip, "D") {
		runSFTP(client, raw, path.Join(*remoteDir, "qiyun-bench-sftp-wide.bin"), 256)
	}
	// F: N independent SSH streams at once. If aggregate throughput rises with N
	// the single stream is window/latency bound; if it stays flat the link is
	// saturated and parallel streams cannot help.
	if want(*skip, "F") {
		for _, n := range parseInts(*parallel) {
			runParallel(client, raw, *remoteDir, n)
		}
	}
	// G: the opposite direction, to see whether the link is asymmetric.
	if want(*skip, "G") {
		runDownload(client, *remoteDir, payload)
	}
}

// runParallel splits the payload across n SSH channels writing to separate
// files, so the total bytes on the wire match a single-stream run.
func runParallel(client *ssh.Client, data []byte, dir string, n int) {
	label := fmt.Sprintf("F %d parallel ssh streams      ", n)
	t0 := time.Now()
	per := len(data) / n
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		chunk := data[i*per : (i+1)*per]
		remote := path.Join(dir, fmt.Sprintf("qiyun-bench-par%d-%d.bin", n, i))
		wg.Add(1)
		go func(chunk []byte, remote string) {
			defer wg.Done()
			sess, err := client.NewSession()
			if err != nil {
				errs <- err
				return
			}
			defer sess.Close()
			stdin, err := sess.StdinPipe()
			if err != nil {
				errs <- err
				return
			}
			if err := sess.Start("cat > " + shellQuote(remote)); err != nil {
				errs <- err
				return
			}
			if _, err := stdin.Write(chunk); err != nil {
				errs <- err
				return
			}
			_ = stdin.Close()
			errs <- sess.Wait()
		}(chunk, remote)
	}
	wg.Wait()
	close(errs)
	var firstErr error
	for err := range errs {
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}
	took := time.Since(t0)
	total := per * n
	fmt.Printf("%s %s  %s  err=%v\n", label, rate(total, took), took.Round(time.Millisecond), firstErr)
	for i := 0; i < n; i++ {
		cleanup(client, path.Join(dir, fmt.Sprintf("qiyun-bench-par%d-%d.bin", n, i)))
	}
}

// runDownload measures remote->local, which needs no stdin pipeline at all.
func runDownload(client *ssh.Client, dir string, payload int) {
	label := "G raw ssh download          "
	remote := path.Join(dir, "qiyun-bench-dl.bin")
	if _, _, err := execOut(client, fmt.Sprintf("dd if=/dev/zero of=%s bs=1M count=%d 2>/dev/null", shellQuote(remote), payload>>20)); err != nil {
		fmt.Printf("%s FAILED  %v\n", label, err)
		return
	}
	defer cleanup(client, remote)
	t0 := time.Now()
	sess, err := client.NewSession()
	if err != nil {
		fmt.Printf("%s FAILED  %v\n", label, err)
		return
	}
	defer sess.Close()
	stdout, err := sess.StdoutPipe()
	if err != nil {
		fmt.Printf("%s FAILED  %v\n", label, err)
		return
	}
	if err := sess.Start("cat " + shellQuote(remote)); err != nil {
		fmt.Printf("%s FAILED  %v\n", label, err)
		return
	}
	n, _ := io.Copy(io.Discard, stdout)
	_ = sess.Wait()
	took := time.Since(t0)
	fmt.Printf("%s %s  %s\n", label, rate(int(n), took), took.Round(time.Millisecond))
}

func runRaw(client *ssh.Client, data []byte, remote string, fsync bool) {
	label := "A raw ssh (cat > file)      "
	cmd := "cat > " + shellQuote(remote)
	if fsync {
		label = "B raw ssh (cat > file + sync)"
		cmd += " && sync"
	}
	t0 := time.Now()
	sess, err := client.NewSession()
	if err != nil {
		fmt.Printf("%s FAILED  %v\n", label, err)
		return
	}
	stdin, err := sess.StdinPipe()
	if err != nil {
		fmt.Printf("%s FAILED  %v\n", label, err)
		return
	}
	if err := sess.Start(cmd); err != nil {
		fmt.Printf("%s FAILED  %v\n", label, err)
		return
	}
	wrote := 0
	for wrote < len(data) {
		n, err := stdin.Write(data[wrote:])
		wrote += n
		if err != nil {
			fmt.Printf("%s FAILED after %d  %v\n", label, wrote, err)
			_ = sess.Close()
			return
		}
	}
	_ = stdin.Close()
	wErr := sess.Wait()
	took := time.Since(t0)
	fmt.Printf("%s %s  %s  wait=%v\n", label, rate(len(data), took), took.Round(time.Millisecond), wErr)
	_ = sess.Close()
	cleanup(client, remote)
}

func runSFTP(client *ssh.Client, data []byte, remote string, requests int) {
	name := "C sftp concurrent/64/WriteAt  "
	if requests > 64 {
		name = fmt.Sprintf("D sftp concurrent/%d/WriteAt ", requests)
	}
	t0 := time.Now()
	sc, err := sftp.NewClient(client,
		sftp.UseConcurrentWrites(true),
		sftp.UseConcurrentReads(true),
		sftp.MaxConcurrentRequestsPerFile(requests),
	)
	if err != nil {
		fmt.Printf("%s FAILED  %v\n", name, err)
		return
	}
	defer sc.Close()
	f, err := sc.Create(remote)
	if err != nil {
		fmt.Printf("%s FAILED  %v\n", name, err)
		return
	}
	const slab = 1 << 20
	off := 0
	for off < len(data) {
		end := off + slab
		if end > len(data) {
			end = len(data)
		}
		if _, werr := f.WriteAt(data[off:end], int64(off)); werr != nil {
			fmt.Printf("%s FAILED at %d  %v\n", name, off, werr)
			_ = f.Close()
			return
		}
		off = end
	}
	cErr := f.Close()
	took := time.Since(t0)
	fmt.Printf("%s %s  %s  close=%v\n", name, rate(len(data), took), took.Round(time.Millisecond), cErr)
	cleanup(client, remote)
}

func execOut(client *ssh.Client, cmd string) (string, int, error) {
	sess, err := client.NewSession()
	if err != nil {
		return "", -1, err
	}
	defer sess.Close()
	out, err := sess.CombinedOutput(cmd)
	code := 0
	if err != nil {
		code = 1
		if ee, ok := err.(*ssh.ExitError); ok {
			code = ee.ExitStatus()
		}
	}
	return string(out), code, err
}

func parseInts(raw string) []int {
	var out []int
	for _, part := range strings.Split(raw, ",") {
		var n int
		if _, err := fmt.Sscanf(strings.TrimSpace(part), "%d", &n); err == nil && n > 0 {
			out = append(out, n)
		}
	}
	return out
}

func want(skip, letter string) bool {
	for _, part := range strings.Split(skip, ",") {
		if strings.EqualFold(strings.TrimSpace(part), letter) {
			return false
		}
	}
	return true
}

func cleanup(client *ssh.Client, remote string) {
	sess, err := client.NewSession()
	if err != nil {
		return
	}
	_ = sess.Run("rm -f " + shellQuote(remote))
	_ = sess.Close()
}

func rate(n int, d time.Duration) string {
	if d <= 0 {
		return "?"
	}
	mbs := float64(n) / (1 << 20) / d.Seconds()
	return fmt.Sprintf("%6.1f MiB/s (%5.1f Mbit/s)", mbs, mbs*8)
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

func must(err error) {
	if err != nil {
		fatal("%v", err)
	}
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "xferbench: "+format+"\n", args...)
	os.Exit(1)
}

var _ = io.Discard
