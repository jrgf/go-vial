package vial

import (
	"io"
	"log/slog"
	"testing"
	"time"
)

func TestOptions(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	config := defaultConfig()
	options := []Option{
		WithLogger(logger),
		WithMaxBodySize(1024),
		WithDisallowUnknownJSONFields(true),
		WithReadHeaderTimeout(time.Second),
		WithReadTimeout(2 * time.Second),
		WithWriteTimeout(3 * time.Second),
		WithIdleTimeout(4 * time.Second),
		WithShutdownTimeout(5 * time.Second),
	}
	for _, option := range options {
		option(&config)
	}

	if config.logger != logger || config.maxBodySize != 1024 || !config.disallowUnknownJSONFields {
		t.Fatal("basic options were not applied")
	}
	if config.readHeaderTimeout != time.Second || config.readTimeout != 2*time.Second || config.writeTimeout != 3*time.Second || config.idleTimeout != 4*time.Second || config.shutdownTimeout != 5*time.Second {
		t.Fatal("timeout options were not applied")
	}

	WithLogger(nil)(&config)
	WithMaxBodySize(0)(&config)
	WithReadHeaderTimeout(-1)(&config)
	WithReadTimeout(-1)(&config)
	WithWriteTimeout(-1)(&config)
	WithIdleTimeout(-1)(&config)
	WithShutdownTimeout(0)(&config)
	if config.logger != logger || config.maxBodySize != 1024 || config.readHeaderTimeout != time.Second || config.readTimeout != 2*time.Second || config.writeTimeout != 3*time.Second || config.idleTimeout != 4*time.Second || config.shutdownTimeout != 5*time.Second {
		t.Fatal("invalid options changed the configuration")
	}
}
