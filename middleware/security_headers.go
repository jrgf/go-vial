package middleware

import (
	"net/http"

	"github.com/jrgf/go-vial"
)

const defaultContentSecurityPolicy = "default-src 'self'; object-src 'none'; base-uri 'self'; frame-ancestors 'none'"

// SecurityHeaders adds restrictive browser security headers. Handlers may
// replace a default before committing the response.
func SecurityHeaders() vial.Middleware {
	return func(next vial.Handler) vial.Handler {
		return func(context *vial.Context) error {
			header := context.Response().Header()
			setHeaderDefault(header, "Content-Security-Policy", defaultContentSecurityPolicy)
			setHeaderDefault(header, "Referrer-Policy", "no-referrer")
			setHeaderDefault(header, "X-Content-Type-Options", "nosniff")
			setHeaderDefault(header, "X-Frame-Options", "DENY")
			if context.Request().TLS != nil {
				setHeaderDefault(header, "Strict-Transport-Security", "max-age=31536000")
			}
			return next(context)
		}
	}
}

func setHeaderDefault(header http.Header, name, value string) {
	if header.Get(name) == "" {
		header.Set(name, value)
	}
}
