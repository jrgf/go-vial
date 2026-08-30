package vial

import (
	"context"
	"io"
	"log"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestOptions(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	serverErrorLog := log.New(io.Discard, "", 0)
	protocols := new(http.Protocols)
	protocols.SetHTTP1(true)
	config := defaultConfig()
	options := []Option{
		WithLogger(logger),
		WithMaxBodySize(1024),
		WithMaxHeaderBytes(2048),
		WithDisallowUnknownJSONFields(true),
		WithReadHeaderTimeout(time.Second),
		WithReadTimeout(2 * time.Second),
		WithWriteTimeout(3 * time.Second),
		WithIdleTimeout(4 * time.Second),
		WithShutdownTimeout(5 * time.Second),
		WithBaseContext(func(net.Listener) context.Context { return context.Background() }),
		WithConnContext(func(contextValue context.Context, _ net.Conn) context.Context { return contextValue }),
		WithHTTPProtocols(protocols),
		WithServerErrorLog(serverErrorLog),
		WithHealthCheckTimeout(6 * time.Second),
	}
	for _, option := range options {
		option(&config)
	}

	if config.logger != logger || config.maxBodySize != 1024 || config.maxHeaderBytes != 2048 || !config.disallowUnknownJSONFields {
		t.Fatal("basic options were not applied")
	}
	if config.readHeaderTimeout != time.Second || config.readTimeout != 2*time.Second || config.writeTimeout != 3*time.Second || config.idleTimeout != 4*time.Second || config.shutdownTimeout != 5*time.Second {
		t.Fatal("timeout options were not applied")
	}
	if config.baseContext == nil || config.connContext == nil || config.protocols != protocols || config.serverErrorLog != serverErrorLog || config.healthCheckTimeout != 6*time.Second {
		t.Fatal("http.Server options were not applied")
	}

	WithLogger(nil)(&config)
	WithMaxBodySize(0)(&config)
	WithMaxHeaderBytes(0)(&config)
	WithReadHeaderTimeout(-1)(&config)
	WithReadTimeout(-1)(&config)
	WithWriteTimeout(-1)(&config)
	WithIdleTimeout(-1)(&config)
	WithShutdownTimeout(0)(&config)
	WithBaseContext(nil)(&config)
	WithConnContext(nil)(&config)
	WithHTTPProtocols(nil)(&config)
	WithServerErrorLog(nil)(&config)
	WithHealthCheckTimeout(0)(&config)
	if config.logger != logger || config.maxBodySize != 1024 || config.readHeaderTimeout != time.Second || config.readTimeout != 2*time.Second || config.writeTimeout != 3*time.Second || config.idleTimeout != 4*time.Second || config.shutdownTimeout != 5*time.Second {
		t.Fatal("invalid options changed the configuration")
	}
	if len(config.optionErrors) != 13 {
		t.Fatalf("invalid options were not collected: %v", config.optionErrors)
	}
}

func TestBuildReportsAllInvalidOptions(t *testing.T) {
	app := New(
		nil,
		WithLogger(nil),
		WithMaxBodySize(0),
		WithMaxHeaderBytes(-1),
		WithReadHeaderTimeout(-1),
		WithReadTimeout(-1),
		WithWriteTimeout(-1),
		WithIdleTimeout(-1),
		WithShutdownTimeout(0),
		WithBaseContext(nil),
		WithConnContext(nil),
		WithHTTPProtocols(nil),
		WithServerErrorLog(nil),
		WithHealthCheckTimeout(0),
		WithReadHeaderTimeout(2*time.Second),
		WithReadTimeout(time.Second),
	)
	err := app.Build()
	if err == nil {
		t.Fatal("expected invalid application configuration")
	}
	message := err.Error()
	for _, name := range []string{
		"Option", "WithLogger", "WithMaxBodySize", "WithMaxHeaderBytes",
		"WithReadHeaderTimeout", "WithReadTimeout", "WithWriteTimeout",
		"WithIdleTimeout", "WithShutdownTimeout",
		"WithBaseContext", "WithConnContext", "WithHTTPProtocols", "WithServerErrorLog",
		"WithHealthCheckTimeout",
	} {
		if !strings.Contains(message, name) {
			t.Fatalf("configuration error does not name %s: %v", name, err)
		}
	}
}

func TestServerRejectsInvalidListenerConfiguration(t *testing.T) {
	app := New()
	for _, address := range []string{"", "missing-port"} {
		if err := app.Run(context.Background(), address); err == nil || !strings.Contains(err.Error(), "Run address") {
			t.Fatalf("address %q returned %v", address, err)
		}
	}
	if err := app.Serve(context.Background(), nil); err == nil || !strings.Contains(err.Error(), "Serve listener") {
		t.Fatalf("nil listener returned %v", err)
	}
}
