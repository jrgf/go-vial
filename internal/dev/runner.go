package dev

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"
)

// Runner owns source watching, sequential builds, and child-process replacement.
type Runner struct {
	config        Config
	builder       *Builder
	watcher       *Watcher
	process       *Process
	processBinary string
}

func NewRunner(config Config) (*Runner, error) {
	normalized, err := config.withDefaults()
	if err != nil {
		return nil, err
	}
	watcher, err := NewWatcher(normalized.Root, normalized.Excludes)
	if err != nil {
		return nil, fmt.Errorf("create source watcher: %w", err)
	}

	return &Runner{
		config:  normalized,
		builder: NewBuilder(normalized),
		watcher: watcher,
	}, nil
}

func (runner *Runner) Run(contextValue context.Context) (runErr error) {
	defer func() {
		runErr = errors.Join(runErr, runner.watcher.Close())
	}()
	defer func() {
		runErr = errors.Join(runErr, runner.stopCurrent())
	}()

	if _, err := fmt.Fprintf(runner.config.Stdout, "[vial] watching %s\n", runner.config.Root); err != nil {
		return fmt.Errorf("write runner status: %w", err)
	}
	if err := runner.rebuildAndSwap(contextValue); err != nil {
		return err
	}

	var debounceTimer *time.Timer
	var debounceChannel <-chan time.Time

	for {
		processDone := runner.processDone()

		select {
		case <-contextValue.Done():
			return nil

		case change, ok := <-runner.watcher.Changes():
			if !ok {
				return nil
			}
			if runner.config.Verbose {
				if _, err := fmt.Fprintf(runner.config.Stdout, "[vial] change detected: %s\n", change.Path); err != nil {
					return fmt.Errorf("write runner status: %w", err)
				}
			}
			if debounceTimer == nil {
				debounceTimer = time.NewTimer(runner.config.Debounce)
			} else {
				if !debounceTimer.Stop() {
					select {
					case <-debounceTimer.C:
					default:
					}
				}
				debounceTimer.Reset(runner.config.Debounce)
			}
			debounceChannel = debounceTimer.C

		case <-debounceChannel:
			debounceChannel = nil
			if _, err := fmt.Fprintln(runner.config.Stdout, "[vial] source change detected; rebuilding"); err != nil {
				return fmt.Errorf("write runner status: %w", err)
			}
			if err := runner.rebuildAndSwap(contextValue); err != nil {
				return err
			}

		case err, ok := <-runner.watcher.Errors():
			if ok && err != nil {
				if _, writeErr := fmt.Fprintf(runner.config.Stderr, "[vial] watcher error: %v\n", err); writeErr != nil {
					return fmt.Errorf("write runner error: %w", writeErr)
				}
			}

		case <-processDone:
			err := runner.process.Err()
			if err != nil {
				if _, writeErr := fmt.Fprintf(runner.config.Stderr, "[vial] application exited: %v\n", err); writeErr != nil {
					return fmt.Errorf("write runner error: %w", writeErr)
				}
			} else {
				if _, writeErr := fmt.Fprintln(runner.config.Stdout, "[vial] application exited"); writeErr != nil {
					return fmt.Errorf("write runner status: %w", writeErr)
				}
			}
			exitedBinary := runner.processBinary
			runner.process = nil
			runner.processBinary = ""
			if exitedBinary != "" {
				_ = os.Remove(exitedBinary)
			}
		}
	}
}

func (runner *Runner) rebuildAndSwap(contextValue context.Context) error {
	var outputErr error
	write := func(writer io.Writer, format string, arguments ...any) {
		if _, err := fmt.Fprintf(writer, format, arguments...); err != nil {
			outputErr = errors.Join(outputErr, fmt.Errorf("write runner output: %w", err))
		}
	}

	candidate, err := runner.builder.Build(contextValue)
	if err != nil {
		write(
			runner.config.Stderr,
			"[vial] build failed; last successful application remains running: %v\n",
			err,
		)
		return outputErr
	}

	oldProcess := runner.process
	oldBinary := runner.processBinary
	if oldProcess != nil {
		write(runner.config.Stdout, "[vial] stopping previous application\n")
		if err := oldProcess.Stop(runner.config.RestartTimeout); err != nil {
			write(runner.config.Stderr, "[vial] could not stop previous application: %v\n", err)
			_ = os.Remove(candidate)
			return outputErr
		}
		runner.process = nil
		runner.processBinary = ""
	}

	process, err := StartProcess(runner.config, candidate)
	if err != nil {
		write(runner.config.Stderr, "[vial] could not start new application: %v\n", err)
		_ = os.Remove(candidate)

		if oldBinary != "" {
			write(runner.config.Stderr, "[vial] attempting to restore previous application\n")
			restored, restoreErr := StartProcess(runner.config, oldBinary)
			if restoreErr != nil {
				write(runner.config.Stderr, "[vial] restore failed: %v\n", restoreErr)
				return outputErr
			}
			runner.process = restored
			runner.processBinary = oldBinary
		}
		return outputErr
	}

	runner.process = process
	runner.processBinary = candidate
	write(runner.config.Stdout, "[vial] application started\n")

	if oldBinary != "" && oldBinary != candidate {
		_ = os.Remove(oldBinary)
	}
	return outputErr
}

func (runner *Runner) processDone() <-chan struct{} {
	if runner.process == nil {
		return nil
	}
	return runner.process.Done()
}

func (runner *Runner) stopCurrent() error {
	if runner.process == nil {
		return nil
	}
	var stopErr error
	if _, err := fmt.Fprintln(runner.config.Stdout, "[vial] stopping application"); err != nil {
		stopErr = errors.Join(stopErr, fmt.Errorf("write runner status: %w", err))
	}
	if err := runner.process.Stop(runner.config.RestartTimeout); err != nil {
		stopErr = errors.Join(stopErr, fmt.Errorf("stop application: %w", err))
		if _, writeErr := fmt.Fprintf(runner.config.Stderr, "[vial] stop failed: %v\n", err); writeErr != nil {
			stopErr = errors.Join(stopErr, fmt.Errorf("write runner error: %w", writeErr))
		}
	}
	if runner.processBinary != "" {
		_ = os.Remove(runner.processBinary)
	}
	runner.process = nil
	runner.processBinary = ""
	return stopErr
}
