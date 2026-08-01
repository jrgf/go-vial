package vial

import (
	"context"
	"errors"
	"fmt"
)

// LifecycleHook runs while the application starts or stops.
type LifecycleHook func(context.Context) error

type lifecycleComponent interface {
	Start(context.Context) error
	Done() <-chan error
	Shutdown(context.Context) error
}

// OnStart registers hooks that run in registration order before components start.
func (app *App) OnStart(hooks ...LifecycleHook) {
	app.mu.Lock()
	defer app.mu.Unlock()
	app.ensureMutableLocked()

	for _, hook := range hooks {
		if hook != nil {
			app.startHooks = append(app.startHooks, hook)
		}
	}
}

// OnStop registers hooks that run in reverse registration order after components stop.
func (app *App) OnStop(hooks ...LifecycleHook) {
	app.mu.Lock()
	defer app.mu.Unlock()
	app.ensureMutableLocked()

	for _, hook := range hooks {
		if hook != nil {
			app.stopHooks = append(app.stopHooks, hook)
		}
	}
}

func (app *App) runLifecycle(parent context.Context, components ...lifecycleComponent) error {
	if parent == nil {
		parent = context.Background()
	}
	if err := app.Build(); err != nil {
		return err
	}

	app.mu.Lock()
	if app.state != applicationBuilt {
		app.mu.Unlock()
		return errors.New("vial: application can only be run once")
	}
	app.state = applicationStarting
	startHooks := append([]LifecycleHook(nil), app.startHooks...)
	stopHooks := append([]LifecycleHook(nil), app.stopHooks...)
	app.mu.Unlock()

	runContext, cancelRun := context.WithCancel(parent)
	defer cancelRun()

	var lifecycleErr error
	for index, hook := range startHooks {
		if err := hook(runContext); err != nil {
			lifecycleErr = fmt.Errorf("startup hook %d: %w", index+1, err)
			break
		}
	}

	started := make([]lifecycleComponent, 0, len(components))
	if lifecycleErr == nil {
		for index, component := range components {
			if component == nil {
				continue
			}
			if err := component.Start(runContext); err != nil {
				lifecycleErr = fmt.Errorf("start component %d: %w", index+1, err)
				break
			}
			started = append(started, component)
		}
	}

	results := make(chan error, len(started))
	for _, component := range started {
		go func() {
			results <- <-component.Done()
		}()
	}

	received := 0
	if lifecycleErr == nil {
		app.setState(applicationRunning)
		if len(started) == 0 {
			<-runContext.Done()
		} else {
			select {
			case <-runContext.Done():
			case err := <-results:
				received++
				lifecycleErr = errors.Join(lifecycleErr, err)
			}
		}
	}

	cancelRun()
	app.setState(applicationStopping)
	shutdownContext, cancelShutdown := context.WithTimeout(
		context.Background(),
		app.config.shutdownTimeout,
	)
	defer cancelShutdown()

	for index := len(started) - 1; index >= 0; index-- {
		lifecycleErr = errors.Join(lifecycleErr, started[index].Shutdown(shutdownContext))
	}
	for received < len(started) {
		select {
		case err := <-results:
			received++
			lifecycleErr = errors.Join(lifecycleErr, err)
		case <-shutdownContext.Done():
			lifecycleErr = errors.Join(lifecycleErr, errors.New("components did not stop before shutdown deadline"))
			received = len(started)
		}
	}
	for index := len(stopHooks) - 1; index >= 0; index-- {
		if err := stopHooks[index](shutdownContext); err != nil {
			lifecycleErr = errors.Join(
				lifecycleErr,
				fmt.Errorf("shutdown hook %d: %w", index+1, err),
			)
		}
	}

	app.setState(applicationStopped)
	return lifecycleErr
}

func (app *App) setState(state applicationState) {
	app.mu.Lock()
	app.state = state
	app.mu.Unlock()
}
