package vial

import "net/http"

// Handler is the framework's HTTP handler contract. Returning errors keeps
// transport error rendering centralized and makes middleware composition simple.
type Handler func(*Context) error

// Middleware wraps a Handler with cross-cutting behavior.
type Middleware func(Handler) Handler

// HTTPMiddleware is the standard net/http middleware contract. Use it for
// tracing and other instrumentation that must wrap the complete HTTP response.
type HTTPMiddleware func(http.Handler) http.Handler

func chain(final Handler, middleware ...Middleware) Handler {
	for i := len(middleware) - 1; i >= 0; i-- {
		final = middleware[i](final)
	}
	return final
}

func chainHTTP(final http.Handler, middleware ...HTTPMiddleware) http.Handler {
	for index := len(middleware) - 1; index >= 0; index-- {
		final = middleware[index](final)
		if final == nil {
			return nil
		}
	}
	return final
}
