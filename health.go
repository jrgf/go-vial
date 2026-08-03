package vial

import (
	"context"
	"fmt"
	"net/http"
)

// HealthCheck reports whether one readiness dependency can currently serve.
type HealthCheck func(context.Context) error

// Health registers a dependency-free liveness endpoint.
func (app *App) Health(path string) {
	app.Get(path, func(context *Context) error {
		return context.NoContent(http.StatusNoContent)
	})
}

// Readiness registers an endpoint that is ready only while the application is
// running and every check succeeds within its configured timeout.
func (app *App) Readiness(path string, checks ...HealthCheck) {
	app.Get(path, app.readinessHandler(checks...))
}

func (app *App) readinessHandler(checks ...HealthCheck) Handler {
	return func(requestContext *Context) error {
		if !app.isReady() {
			return requestContext.NoContent(http.StatusServiceUnavailable)
		}
		for index, check := range checks {
			checkContext, cancel := context.WithTimeout(requestContext.Request().Context(), app.config.healthCheckTimeout)
			err := runHealthCheck(checkContext, check)
			cancel()
			if err != nil {
				requestContext.Logger().Warn("readiness check failed", "check", index+1, "error", err)
				return requestContext.NoContent(http.StatusServiceUnavailable)
			}
		}
		return requestContext.NoContent(http.StatusNoContent)
	}
}

func (app *App) isReady() bool {
	app.mu.RLock()
	ready := app.state == applicationRunning
	app.mu.RUnlock()
	return ready
}

func runHealthCheck(contextValue context.Context, check HealthCheck) (err error) {
	if check == nil {
		return fmt.Errorf("health check is nil")
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("health check panicked")
		}
	}()
	return check(contextValue)
}
