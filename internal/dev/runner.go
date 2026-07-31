package dev

import (
	"context"
	"fmt"
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

func (runner *Runner) Run(contextValue context.Context) error {
	defer runner.watcher.Close()
	defer runner.stopCurrent()

	fmt.Fprintf(runner.config.Stdout, "[vial] watching %s\n", runner.config.Root)
	runner.rebuildAndSwap(contextValue)

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
				fmt.Fprintf(runner.config.Stdout, "[vial] change detected: %s\n", change.Path)
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
			fmt.Fprintln(runner.config.Stdout, "[vial] source change detected; rebuilding")
			runner.rebuildAndSwap(contextValue)

		case err, ok := <-runner.watcher.Errors():
			if ok && err != nil {
				fmt.Fprintf(runner.config.Stderr, "[vial] watcher error: %v\n", err)
			}

		case <-processDone:
			err := runner.process.Err()
			if err != nil {
				fmt.Fprintf(runner.config.Stderr, "[vial] application exited: %v\n", err)
			} else {
				fmt.Fprintln(runner.config.Stdout, "[vial] application exited")
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

func (runner *Runner) rebuildAndSwap(contextValue context.Context) {
	candidate, err := runner.builder.Build(contextValue)
	if err != nil {
		fmt.Fprintf(
			runner.config.Stderr,
			"[vial] build failed; last successful application remains running: %v\n",
			err,
		)
		return
	}

	oldProcess := runner.process
	oldBinary := runner.processBinary
	if oldProcess != nil {
		fmt.Fprintln(runner.config.Stdout, "[vial] stopping previous application")
		if err := oldProcess.Stop(runner.config.RestartTimeout); err != nil {
			fmt.Fprintf(runner.config.Stderr, "[vial] could not stop previous application: %v\n", err)
			_ = os.Remove(candidate)
			return
		}
		runner.process = nil
		runner.processBinary = ""
	}

	process, err := StartProcess(runner.config, candidate)
	if err != nil {
		fmt.Fprintf(runner.config.Stderr, "[vial] could not start new application: %v\n", err)
		_ = os.Remove(candidate)

		if oldBinary != "" {
			fmt.Fprintln(runner.config.Stderr, "[vial] attempting to restore previous application")
			restored, restoreErr := StartProcess(runner.config, oldBinary)
			if restoreErr != nil {
				fmt.Fprintf(runner.config.Stderr, "[vial] restore failed: %v\n", restoreErr)
				return
			}
			runner.process = restored
			runner.processBinary = oldBinary
		}
		return
	}

	runner.process = process
	runner.processBinary = candidate
	fmt.Fprintln(runner.config.Stdout, "[vial] application started")

	if oldBinary != "" && oldBinary != candidate {
		_ = os.Remove(oldBinary)
	}
}

func (runner *Runner) processDone() <-chan struct{} {
	if runner.process == nil {
		return nil
	}
	return runner.process.Done()
}

func (runner *Runner) stopCurrent() {
	if runner.process == nil {
		return
	}
	fmt.Fprintln(runner.config.Stdout, "[vial] stopping application")
	if err := runner.process.Stop(runner.config.RestartTimeout); err != nil {
		fmt.Fprintf(runner.config.Stderr, "[vial] stop failed: %v\n", err)
	}
	if runner.processBinary != "" {
		_ = os.Remove(runner.processBinary)
	}
	runner.process = nil
	runner.processBinary = ""
}
