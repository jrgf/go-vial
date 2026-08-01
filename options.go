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

func WithLogger(logger *slog.Logger) Option {
	return func(cfg *config) {
		if logger != nil {
			cfg.logger = logger
		}
	}
}

func WithMaxBodySize(bytes int64) Option {
	return func(cfg *config) {
		if bytes > 0 {
			cfg.maxBodySize = bytes
		}
	}
}

func WithDisallowUnknownJSONFields(enabled bool) Option {
	return func(cfg *config) {
		cfg.disallowUnknownJSONFields = enabled
	}
}

func WithReadHeaderTimeout(timeout time.Duration) Option {
	return func(cfg *config) {
		if timeout >= 0 {
			cfg.readHeaderTimeout = timeout
		}
	}
}

func WithReadTimeout(timeout time.Duration) Option {
	return func(cfg *config) {
		if timeout >= 0 {
			cfg.readTimeout = timeout
		}
	}
}

func WithWriteTimeout(timeout time.Duration) Option {
	return func(cfg *config) {
		if timeout >= 0 {
			cfg.writeTimeout = timeout
		}
	}
}

func WithIdleTimeout(timeout time.Duration) Option {
	return func(cfg *config) {
		if timeout >= 0 {
			cfg.idleTimeout = timeout
		}
	}
}

func WithShutdownTimeout(timeout time.Duration) Option {
	return func(cfg *config) {
		if timeout > 0 {
			cfg.shutdownTimeout = timeout
		}
	}
}
