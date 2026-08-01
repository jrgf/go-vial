package vial

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
)

// Task is application work that runs until its context is canceled or it fails.
type Task func(context.Context) error

// TaskOption configures a background task.
type TaskOption struct {
	nonCritical bool
}

// NonCritical keeps the application running when the task exits or fails.
func NonCritical() TaskOption {
	return TaskOption{nonCritical: true}
}

type taskDefinition struct {
	name     string
	task     Task
	critical bool
}

// Go registers a named background task. Tasks are critical by default and
// begin running when the application starts.
func (app *App) Go(name string, task Task, options ...TaskOption) {
	app.mu.Lock()
	defer app.mu.Unlock()
	app.ensureMutableLocked()

	definition := taskDefinition{name: name, task: task, critical: true}
	for _, option := range options {
		if option.nonCritical {
			definition.critical = false
		}
	}
	app.tasks = append(app.tasks, definition)
}

func validateTasks(tasks []taskDefinition) error {
	names := make(map[string]struct{}, len(tasks))
	for _, definition := range tasks {
		if !validRegistrationName(definition.name) {
			return fmt.Errorf("invalid task name %q", definition.name)
		}
		if definition.task == nil {
			return fmt.Errorf("task %q is nil", definition.name)
		}
		if _, exists := names[definition.name]; exists {
			return fmt.Errorf("duplicate task name %q", definition.name)
		}
		names[definition.name] = struct{}{}
	}
	return nil
}

type taskResult struct {
	definition taskDefinition
	err        error
	panicValue any
}

type taskSupervisor struct {
	tasks   []taskDefinition
	logger  *slog.Logger
	done    chan error
	results chan taskResult

	finished        chan struct{}
	monitorFinished chan struct{}
	completeOnce    sync.Once
	abortOnce       sync.Once
	abort           chan struct{}

	runningMu sync.Mutex
	running   map[string]struct{}
}

func newTaskSupervisor(tasks []taskDefinition, logger *slog.Logger) *taskSupervisor {
	if logger == nil {
		logger = slog.Default()
	}
	return &taskSupervisor{
		tasks:           tasks,
		logger:          logger,
		done:            make(chan error, 1),
		results:         make(chan taskResult, len(tasks)),
		finished:        make(chan struct{}),
		monitorFinished: make(chan struct{}),
		abort:           make(chan struct{}),
		running:         make(map[string]struct{}, len(tasks)),
	}
}

func (supervisor *taskSupervisor) Start(contextValue context.Context) error {
	for _, definition := range supervisor.tasks {
		supervisor.running[definition.name] = struct{}{}
	}
	for _, definition := range supervisor.tasks {
		go supervisor.run(contextValue, definition)
	}
	go supervisor.monitor(contextValue)
	return nil
}

func (supervisor *taskSupervisor) Done() <-chan error {
	return supervisor.done
}

func (supervisor *taskSupervisor) Shutdown(contextValue context.Context) error {
	select {
	case <-supervisor.finished:
		select {
		case <-supervisor.monitorFinished:
			return nil
		case <-contextValue.Done():
		}
	case <-contextValue.Done():
	}

	names := supervisor.runningNames()
	if len(names) == 0 {
		<-supervisor.monitorFinished
		return nil
	}
	err := fmt.Errorf(
		"background tasks did not stop before shutdown deadline: %s",
		strings.Join(names, ", "),
	)
	supervisor.abortOnce.Do(func() { close(supervisor.abort) })
	supervisor.complete(nil)
	<-supervisor.monitorFinished
	return err
}

func (supervisor *taskSupervisor) run(contextValue context.Context, definition taskDefinition) {
	result := executeTask(contextValue, definition)
	supervisor.runningMu.Lock()
	delete(supervisor.running, definition.name)
	finished := len(supervisor.running) == 0
	supervisor.runningMu.Unlock()
	if finished {
		close(supervisor.finished)
	}
	supervisor.results <- result
}

func executeTask(contextValue context.Context, definition taskDefinition) (result taskResult) {
	result.definition = definition
	defer func() {
		if recovered := recover(); recovered != nil {
			result.panicValue = recovered
		}
	}()
	result.err = definition.task(contextValue)
	return result
}

func (supervisor *taskSupervisor) monitor(contextValue context.Context) {
	defer close(supervisor.monitorFinished)
	remaining := len(supervisor.tasks)
	contextDone := contextValue.Done()
	stopping := false
	var shutdownErr error

	for remaining > 0 {
		select {
		case <-supervisor.abort:
			return
		case <-contextDone:
			stopping = true
			contextDone = nil
		case result := <-supervisor.results:
			remaining--
			if contextValue.Err() != nil {
				stopping = true
				contextDone = nil
			}
			err := taskOutcome(result, stopping, contextValue.Err())
			if err == nil {
				continue
			}
			if result.definition.critical {
				if !stopping {
					supervisor.complete(err)
					return
				}
				shutdownErr = errors.Join(shutdownErr, err)
				continue
			}
			supervisor.logger.Error(
				"background task stopped",
				"task", result.definition.name,
				"error", err,
			)
		}
	}

	if !stopping {
		<-contextDone
	}
	supervisor.complete(shutdownErr)
}

func taskOutcome(result taskResult, stopping bool, contextErr error) error {
	if result.panicValue != nil {
		if err, ok := result.panicValue.(error); ok {
			return fmt.Errorf("background task %q panicked: %w", result.definition.name, err)
		}
		return fmt.Errorf("background task %q panicked: %v", result.definition.name, result.panicValue)
	}
	if result.err != nil {
		if stopping && contextErr != nil && errors.Is(result.err, contextErr) {
			return nil
		}
		return fmt.Errorf("background task %q failed: %w", result.definition.name, result.err)
	}
	if !stopping {
		return fmt.Errorf("background task %q exited unexpectedly", result.definition.name)
	}
	return nil
}

func (supervisor *taskSupervisor) complete(err error) {
	supervisor.completeOnce.Do(func() {
		supervisor.done <- err
	})
}

func (supervisor *taskSupervisor) runningNames() []string {
	supervisor.runningMu.Lock()
	defer supervisor.runningMu.Unlock()

	names := make([]string, 0, len(supervisor.running))
	for _, definition := range supervisor.tasks {
		if _, running := supervisor.running[definition.name]; running {
			names = append(names, definition.name)
		}
	}
	return names
}
