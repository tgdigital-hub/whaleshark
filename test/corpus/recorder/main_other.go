//go:build !darwin && !linux

package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "recorder: recordings are made on macOS and Linux only")
	os.Exit(2)
}
