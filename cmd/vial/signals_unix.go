//go:build !windows

package main

import (
	"os"
	"syscall"
)

func developmentSignals() []os.Signal {
	return []os.Signal{os.Interrupt, syscall.SIGTERM}
}
