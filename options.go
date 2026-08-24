package vial

import (
	"context"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"time"
)

const (
	defaultMaxBodySize        = int64(16 << 20) // 16 MiB
	defaultMaxHeaderBytes     = http.DefaultMaxHeaderBytes
	defaultReadHeaderTimeout  = 5 * time.Second
	defaultReadTimeout        = 15 * time.Second
	defaultWriteTimeout       = 30 * time.Second
	defaultIdleTimeout        = 60 * time.Second
	defaultShutdownTimeout    = 10 * time.Second
	defaultHealthCheckTimeout = 2 * time.Second
)

type config struct {
	logger                    *slog.Logger
	maxBodySize               int64
	maxHeaderBytes            int
	disallowUnknownJSONFields bool
	readHeaderTimeout         time.Duration
	readTimeout               time.Duration
	writeTimeout              time.Duration
	idleTimeout               time.Duration
	shutdownTimeout           time.Duration
	optionErrors              []error
	trustedProxies            []netip.Prefix
	baseContext               func(net.Listener) context.Context
	connContext               func(context.Context, net.Conn) context.Context
	serverErrorLog            *log.Logger
	healthCheckTimeout        time.Duration
}

func defaultConfig() config {
	return config{
		logger:             slog.Default(),
		maxBodySize:        defaultMaxBodySize,
		maxHeaderBytes:     defaultMaxHeaderBytes,
		readHeaderTimeout:  defaultReadHeaderTimeout,
		readTimeout:        defaultReadTimeout,
		writeTimeout:       defaultWriteTimeout,
		idleTimeout:        defaultIdleTimeout,
		shutdownTimeout:    defaultShutdownTimeout,
		healthCheckTimeout: defaultHealthCheckTimeout,
	}
}

func (cfg *config) invalidOption(name, message string) {
	cfg.optionErrors = append(cfg.optionErrors, fmt.Errorf("%s: %s", name, message))
}

func (cfg *config) validate() error {
	errorsFound := append([]error(nil), cfg.optionErrors...)
	if cfg.readTimeout > 0 && cfg.readHeaderTimeout > cfg.readTimeout {
		errorsFound = append(errorsFound, fmt.Errorf("WithReadHeaderTimeout: must not exceed WithReadTimeout"))
	}
	return errors.Join(errorsFound...)
}

// Option configures an App.
type Option func(*config)

// WithLogger sets the application logger.
func WithLogger(logger *slog.Logger) Option {
	return func(cfg *config) {
		if logger == nil {
			cfg.invalidOption("WithLogger", "logger cannot be nil")
			return
		}
		cfg.logger = logger
	}
}

// WithMaxBodySize sets the request body limit when bytes is positive.
func WithMaxBodySize(bytes int64) Option {
	return func(cfg *config) {
		if bytes <= 0 {
			cfg.invalidOption("WithMaxBodySize", "bytes must be greater than zero")
			return
		}
		cfg.maxBodySize = bytes
	}
}

// WithMaxHeaderBytes sets the maximum request-header size.
func WithMaxHeaderBytes(bytes int) Option {
	return func(cfg *config) {
		if bytes <= 0 {
			cfg.invalidOption("WithMaxHeaderBytes", "bytes must be greater than zero")
			return
		}
		cfg.maxHeaderBytes = bytes
	}
}

// WithTrustedProxies trusts forwarding headers only when the direct peer
// matches one of the listed IPs or CIDRs. The default trusts none.
func WithTrustedProxies(values ...string) Option {
	return func(cfg *config) {
		for _, value := range values {
			value = strings.TrimSpace(value)
			prefix, err := netip.ParsePrefix(value)
			if err != nil {
				address, addressErr := netip.ParseAddr(value)
				if addressErr != nil {
					cfg.invalidOption("WithTrustedProxies", fmt.Sprintf("invalid IP or CIDR %q", value))
					continue
				}
				address = address.Unmap()
				prefix = netip.PrefixFrom(address, address.BitLen())
			} else {
				prefix = prefix.Masked()
			}
			cfg.trustedProxies = append(cfg.trustedProxies, prefix)
		}
	}
}

// WithBaseContext sets http.Server.BaseContext.
func WithBaseContext(baseContext func(net.Listener) context.Context) Option {
	return func(cfg *config) {
		if baseContext == nil {
			cfg.invalidOption("WithBaseContext", "function cannot be nil")
			return
		}
		cfg.baseContext = baseContext
	}
}

// WithConnContext sets http.Server.ConnContext.
func WithConnContext(connContext func(context.Context, net.Conn) context.Context) Option {
	return func(cfg *config) {
		if connContext == nil {
			cfg.invalidOption("WithConnContext", "function cannot be nil")
			return
		}
		cfg.connContext = connContext
	}
}

// WithServerErrorLog sets http.Server.ErrorLog.
func WithServerErrorLog(logger *log.Logger) Option {
	return func(cfg *config) {
		if logger == nil {
			cfg.invalidOption("WithServerErrorLog", "logger cannot be nil")
			return
		}
		cfg.serverErrorLog = logger
	}
}

// WithHealthCheckTimeout sets the maximum duration of each readiness check.
func WithHealthCheckTimeout(timeout time.Duration) Option {
	return func(cfg *config) {
		if timeout <= 0 {
			cfg.invalidOption("WithHealthCheckTimeout", "timeout must be greater than zero")
			return
		}
		cfg.healthCheckTimeout = timeout
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
		if timeout < 0 {
			cfg.invalidOption("WithReadHeaderTimeout", "timeout cannot be negative")
			return
		}
		cfg.readHeaderTimeout = timeout
	}
}

// WithReadTimeout sets the HTTP server request-read timeout.
func WithReadTimeout(timeout time.Duration) Option {
	return func(cfg *config) {
		if timeout < 0 {
			cfg.invalidOption("WithReadTimeout", "timeout cannot be negative")
			return
		}
		cfg.readTimeout = timeout
	}
}

// WithWriteTimeout sets the HTTP server response-write timeout.
func WithWriteTimeout(timeout time.Duration) Option {
	return func(cfg *config) {
		if timeout < 0 {
			cfg.invalidOption("WithWriteTimeout", "timeout cannot be negative")
			return
		}
		cfg.writeTimeout = timeout
	}
}

// WithIdleTimeout sets the HTTP server keep-alive idle timeout.
func WithIdleTimeout(timeout time.Duration) Option {
	return func(cfg *config) {
		if timeout < 0 {
			cfg.invalidOption("WithIdleTimeout", "timeout cannot be negative")
			return
		}
		cfg.idleTimeout = timeout
	}
}

// WithShutdownTimeout sets the graceful shutdown deadline when timeout is positive.
func WithShutdownTimeout(timeout time.Duration) Option {
	return func(cfg *config) {
		if timeout <= 0 {
			cfg.invalidOption("WithShutdownTimeout", "timeout must be greater than zero")
			return
		}
		cfg.shutdownTimeout = timeout
	}
}
