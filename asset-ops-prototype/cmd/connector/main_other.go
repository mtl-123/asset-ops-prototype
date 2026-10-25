//go:build !windows

package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "qiyun-connector is Windows-only")
	os.Exit(1)
}
