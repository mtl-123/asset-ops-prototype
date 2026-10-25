package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	base, err := os.Getwd()
	if err != nil {
		fatal(err)
	}
	css := join(base, []string{"styles.css", "platform.css", "monitoring.css", "terminal-workspace.css", "admin-icons.css", "admin-theme.css"})
	shell := read(filepath.Join(base, "admin-shell.html"))
	// Data layer first: modules below subscribe to window.QiyunAPI/HostMetrics at
	// load time, and optional chaining would silently drop the subscription if
	// they loaded before it existed (that is how "主机与远程" once showed 0 hosts).
	js := join(base, []string{"core.js", "terminal-gateway.js", "host-metrics.js", "business.js", "infra.js", "terminal-workspace.js", "transfer-ui.js", "platform.js", "monitoring-model.js", "monitoring.js", "admin-shell.js"})
	fragment := "<style>\n" + css + "\n</style>\n" + shell + "\n<script>\n" + js + "\nAssetDemo.init();\n</script>\n"
	page := "<!doctype html>\n<html lang=\"zh-CN\"><head><meta charset=\"utf-8\"><meta name=\"viewport\" content=\"width=device-width,initial-scale=1\"><title>栖云 · 资产应用与运维</title><link rel=\"stylesheet\" href=\"/vendor/xterm/xterm.css\"><script src=\"/vendor/xterm/xterm.js\"></script><script src=\"/vendor/xterm/addon-fit.js\"></script><script src=\"/vendor/trzsz/trzsz.js\"></script><style>html,body{margin:0;padding:0}body{background:#f6f8fa}#asset-app{border-radius:0}</style></head><body>\n" + fragment + "\n</body></html>\n"
	write(filepath.Join(base, "index.html"), page)
	if out := strings.TrimSpace(os.Getenv("QIYUN_INLINE_OUTPUT")); out != "" {
		write(out, strings.Replace(fragment, "class=\"vab-shell\"", "class=\"vab-shell vab-inline\"", 1))
		fmt.Println("Built inline:", out)
	}
	fmt.Println("Built preview:", filepath.Join(base, "index.html"))
	fmt.Println("Inline bytes:", len([]byte(fragment)))
}
func join(base string, names []string) string {
	parts := make([]string, 0, len(names))
	for _, name := range names {
		parts = append(parts, read(filepath.Join(base, name)))
	}
	return strings.Join(parts, "\n")
}
func read(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		fatal(fmt.Errorf("read %s: %w", path, err))
	}
	return string(b)
}
func write(path, content string) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		fatal(err)
	}
}
func fatal(err error) { fmt.Fprintln(os.Stderr, err); os.Exit(1) }
