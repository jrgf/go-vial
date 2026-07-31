//go:build windows

package main

import "os"

func developmentSignals() []os.Signal {
	return []os.Signal{os.Interrupt}
}
