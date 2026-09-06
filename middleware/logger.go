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
			var requestErr error
			if err := context.AfterResponse(func() {
				status := context.Status()
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
				if requestErr != nil {
					attributes = append(attributes, "error", requestErr)
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
			}); err != nil {
				return err
			}
			requestErr = next(context)
			return requestErr
		}
	}
}
