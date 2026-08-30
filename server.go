package vial

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
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
	if strings.TrimSpace(address) == "" {
		return fmt.Errorf("Run address: address cannot be empty")
	}
	if _, _, err := net.SplitHostPort(address); err != nil {
		return fmt.Errorf("Run address %q: %w", address, err)
	}
	if err := app.Build(); err != nil {
		return err
	}

	listener, err := net.Listen("tcp", address)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", address, err)
	}
	return app.Serve(contextValue, listener)
}

// Serve runs the application on an existing listener. This is useful for tests
// and for callers that need custom listener configuration.
func (app *App) Serve(contextValue context.Context, listener net.Listener) (serveErr error) {
	if listener == nil {
		return fmt.Errorf("Serve listener: listener cannot be nil")
	}
	if err := app.Build(); err != nil {
		return err
	}
	if contextValue == nil {
		contextValue = context.Background()
	}
	defer func() {
		if err := listener.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			serveErr = errors.Join(serveErr, fmt.Errorf("close listener: %w", err))
		}
	}()

	runContext, stopSignals := signal.NotifyContext(contextValue, shutdownSignals()...)
	defer stopSignals()
	baseContext := app.config.baseContext
	if baseContext == nil {
		baseContext = func(net.Listener) context.Context { return runContext }
	}

	component := &httpComponent{
		listener: listener,
		server: &http.Server{
			Handler:           app,
			ReadHeaderTimeout: app.config.readHeaderTimeout,
			ReadTimeout:       app.config.readTimeout,
			WriteTimeout:      app.config.writeTimeout,
			IdleTimeout:       app.config.idleTimeout,
			MaxHeaderBytes:    app.config.maxHeaderBytes,
			BaseContext:       baseContext,
			ConnContext:       app.config.connContext,
			Protocols:         app.config.protocols,
			ErrorLog:          app.config.serverErrorLog,
		},
		done:   make(chan error, 1),
		logger: app.config.logger,
	}
	return app.runLifecycle(runContext, component)
}

type httpComponent struct {
	listener net.Listener
	server   *http.Server
	done     chan error
	logger   *slog.Logger
}

func (component *httpComponent) Start(context.Context) error {
	go func() {
		err := component.server.Serve(component.listener)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		} else if err != nil {
			err = fmt.Errorf("serve HTTP: %w", err)
		}
		component.done <- err
	}()
	component.logger.Info("HTTP server started", "address", component.listener.Addr().String())
	return nil
}

func (component *httpComponent) Done() <-chan error {
	return component.done
}

func (component *httpComponent) Shutdown(contextValue context.Context) error {
	if err := component.server.Shutdown(contextValue); err != nil {
		return errors.Join(
			fmt.Errorf("graceful shutdown: %w", err),
			component.server.Close(),
		)
	}
	component.logger.Info("HTTP server stopped")
	return nil
}
