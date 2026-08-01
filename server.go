package vial

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"time"
)

const routesOutputEnvironment = "VIAL_ROUTES_OUTPUT"

// Run listens on address and serves until the parent context or a supported OS
// shutdown signal is received.
func (app *App) Run(contextValue context.Context, address string) error {
	if output := os.Getenv(routesOutputEnvironment); output != "" {
		routes, err := app.Routes()
		if err != nil {
			return err
		}
		data, err := json.Marshal(routes)
		if err != nil {
			return fmt.Errorf("encode routes: %w", err)
		}
		if err := os.WriteFile(output, append(data, '\n'), 0o600); err != nil {
			return fmt.Errorf("write routes: %w", err)
		}
		return nil
	}

	listener, err := net.Listen("tcp", address)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", address, err)
	}
	return app.Serve(contextValue, listener)
}

// Serve runs the application on an existing listener. This is useful for tests
// and for callers that need custom listener configuration.
func (app *App) Serve(contextValue context.Context, listener net.Listener) error {
	if contextValue == nil {
		contextValue = context.Background()
	}
	if err := app.Build(); err != nil {
		_ = listener.Close()
		return err
	}

	runContext, stopSignals := signal.NotifyContext(contextValue, shutdownSignals()...)
	defer stopSignals()

	server := &http.Server{
		Handler:           app,
		ReadHeaderTimeout: app.config.readHeaderTimeout,
		ReadTimeout:       app.config.readTimeout,
		WriteTimeout:      app.config.writeTimeout,
		IdleTimeout:       app.config.idleTimeout,
	}

	serverErrors := make(chan error, 1)
	go func() {
		serverErrors <- server.Serve(listener)
	}()

	app.config.logger.Info("HTTP server started", "address", listener.Addr().String())

	select {
	case err := <-serverErrors:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("serve HTTP: %w", err)

	case <-runContext.Done():
		shutdownContext, cancel := context.WithTimeout(
			context.Background(),
			app.config.shutdownTimeout,
		)
		defer cancel()

		shutdownErr := server.Shutdown(shutdownContext)
		if shutdownErr != nil {
			closeErr := server.Close()
			return errors.Join(
				fmt.Errorf("graceful shutdown: %w", shutdownErr),
				closeErr,
			)
		}

		select {
		case err := <-serverErrors:
			if err != nil && !errors.Is(err, http.ErrServerClosed) {
				return fmt.Errorf("serve HTTP during shutdown: %w", err)
			}
		case <-time.After(app.config.shutdownTimeout):
			return errors.New("HTTP server did not stop after graceful shutdown")
		}

		app.config.logger.Info("HTTP server stopped")
		return nil
	}
}
