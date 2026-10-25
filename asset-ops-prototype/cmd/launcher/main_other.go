//go:build !windows

// The launcher is Windows-only because it starts mstsc and uses Windows
// credential/certificate stores. Keep the package buildable on other systems so
// `go test ./...` still works on Linux CI.
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "qiyun-launcher is Windows-only")
	os.Exit(1)
}
