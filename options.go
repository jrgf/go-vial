package vial

import (
	"log/slog"
	"time"
)

const (
	defaultMaxBodySize       = int64(16 << 20) // 16 MiB
	defaultReadHeaderTimeout = 5 * time.Second
	defaultReadTimeout       = 15 * time.Second
	defaultWriteTimeout      = 30 * time.Second
	defaultIdleTimeout       = 60 * time.Second
	defaultShutdownTimeout   = 10 * time.Second
)

type config struct {
	logger                    *slog.Logger
	maxBodySize               int64
	disallowUnknownJSONFields bool
	readHeaderTimeout         time.Duration
	readTimeout               time.Duration
	writeTimeout              time.Duration
	idleTimeout               time.Duration
	shutdownTimeout           time.Duration
}

func defaultConfig() config {
	return config{
		logger:            slog.Default(),
		maxBodySize:       defaultMaxBodySize,
		readHeaderTimeout: defaultReadHeaderTimeout,
		readTimeout:       defaultReadTimeout,
		writeTimeout:      defaultWriteTimeout,
		idleTimeout:       defaultIdleTimeout,
		shutdownTimeout:   defaultShutdownTimeout,
	}
}

// Option configures an App.
type Option func(*config)

// WithLogger sets the application logger when logger is non-nil.
func WithLogger(logger *slog.Logger) Option {
	return func(cfg *config) {
		if logger != nil {
			cfg.logger = logger
		}
	}
}

// WithMaxBodySize sets the request body limit when bytes is positive.
func WithMaxBodySize(bytes int64) Option {
	return func(cfg *config) {
		if bytes > 0 {
			cfg.maxBodySize = bytes
		}
	}
}

// WithDisallowUnknownJSONFields controls rejection of unknown JSON fields.
func WithDisallowUnknownJSONFields(enabled bool) Option {
	return func(cfg *config) {
		cfg.disallowUnknownJSONFields = enabled
	}
}

// WithReadHeaderTimeout sets the HTTP server header-read timeout.
func WithReadHeaderTimeout(timeout time.Duration) Option {
	return func(cfg *config) {
		if timeout >= 0 {
			cfg.readHeaderTimeout = timeout
		}
	}
}

// WithReadTimeout sets the HTTP server request-read timeout.
func WithReadTimeout(timeout time.Duration) Option {
	return func(cfg *config) {
		if timeout >= 0 {
			cfg.readTimeout = timeout
		}
	}
}

// WithWriteTimeout sets the HTTP server response-write timeout.
func WithWriteTimeout(timeout time.Duration) Option {
	return func(cfg *config) {
		if timeout >= 0 {
			cfg.writeTimeout = timeout
		}
	}
}

// WithIdleTimeout sets the HTTP server keep-alive idle timeout.
func WithIdleTimeout(timeout time.Duration) Option {
	return func(cfg *config) {
		if timeout >= 0 {
			cfg.idleTimeout = timeout
		}
	}
}

// WithShutdownTimeout sets the graceful shutdown deadline when timeout is positive.
func WithShutdownTimeout(timeout time.Duration) Option {
	return func(cfg *config) {
		if timeout > 0 {
			cfg.shutdownTimeout = timeout
		}
	}
}
