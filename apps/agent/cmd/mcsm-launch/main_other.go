//go:build !linux

// Command mcsm-launch is Linux only; see main.go.
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "mcsm-launch: only supported on Linux")
	os.Exit(1)
}
