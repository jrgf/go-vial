//go:build windows

package dev

import (
	"os/exec"
	"syscall"
)

func configureCommand(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP,
	}
}

func interruptCommand(command *exec.Cmd) error {
	if command.Process == nil {
		return nil
	}
	// Go cannot send os.Interrupt to a Windows child process through its portable
	// API. Kill the process instead.
	return command.Process.Kill()
}

func killCommand(command *exec.Cmd) error {
	if command.Process == nil {
		return nil
	}
	return command.Process.Kill()
}
