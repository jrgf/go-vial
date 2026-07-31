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
	// os.Interrupt is not implemented for Windows child processes in the same
	// portable way as Unix signals, so the MVP terminates the child directly.
	return command.Process.Kill()
}

func killCommand(command *exec.Cmd) error {
	if command.Process == nil {
		return nil
	}
	return command.Process.Kill()
}
