package dev

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"time"
)

// Process represents a running application child process.
type Process struct {
	command *exec.Cmd
	done    chan struct{}

	mu      sync.RWMutex
	waitErr error
}

func StartProcess(config Config, executable string) (*Process, error) {
	// The runner, rather than os/exec, owns shutdown so it can first send a
	// graceful interrupt and only kill after the restart timeout.
	command := exec.Command(executable, config.AppArgs...)
	command.Dir = config.Root
	command.Env = append(config.Env, "VIAL_ENV=development")
	command.Stdin = config.Stdin
	command.Stdout = config.Stdout
	command.Stderr = config.Stderr
	configureCommand(command)

	if err := command.Start(); err != nil {
		return nil, fmt.Errorf("start application: %w", err)
	}

	process := &Process{command: command, done: make(chan struct{})}
	go func() {
		err := command.Wait()
		process.mu.Lock()
		process.waitErr = err
		process.mu.Unlock()
		close(process.done)
	}()
	return process, nil
}

func (process *Process) Done() <-chan struct{} {
	return process.done
}

func (process *Process) Err() error {
	process.mu.RLock()
	defer process.mu.RUnlock()
	return process.waitErr
}

func (process *Process) Stop(timeout time.Duration) error {
	select {
	case <-process.done:
		return nil
	default:
	}

	// Interruption is best-effort; the timeout/kill path handles failures.
	_ = interruptCommand(process.command)

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case <-process.done:
		return nil
	case <-timer.C:
	}

	killErr := killCommand(process.command)
	if killErr != nil && !errors.Is(killErr, os.ErrProcessDone) {
		return fmt.Errorf("kill application process: %w", killErr)
	}

	select {
	case <-process.done:
		return nil
	case <-time.After(timeout):
		return errors.New("application process did not exit after kill")
	}
}
