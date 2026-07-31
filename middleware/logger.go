package middleware

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/jrgf/go-vial"
)

// Logger emits one structured completion record for each request.
func Logger() vial.Middleware {
	return func(next vial.Handler) vial.Handler {
		return func(context *vial.Context) error {
			started := time.Now()
			err := next(context)

			status := context.Status()
			if err != nil && !context.Committed() {
				status = vial.StatusCode(err)
			}

			attributes := []any{
				"method", context.Request().Method,
				"path", context.Request().URL.Path,
				"status", status,
				"bytes", context.BytesWritten(),
				"duration", time.Since(started),
				"remote_addr", context.Request().RemoteAddr,
			}
			if route := context.Route(); route != nil {
				attributes = append(attributes, "route", route.Pattern)
			}
			if err != nil {
				attributes = append(attributes, "error", err)
			}

			level := slog.LevelInfo
			switch {
			case status >= http.StatusInternalServerError:
				level = slog.LevelError
			case status >= http.StatusBadRequest:
				level = slog.LevelWarn
			}

			context.Logger().Log(
				context.Request().Context(),
				level,
				"HTTP request completed",
				attributes...,
			)
			return err
		}
	}
}
