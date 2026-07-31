package vial

// Handler is the framework's HTTP handler contract. Returning errors keeps
// transport error rendering centralized and makes middleware composition simple.
type Handler func(*Context) error

// Middleware wraps a Handler with cross-cutting behavior.
type Middleware func(Handler) Handler

func chain(final Handler, middleware ...Middleware) Handler {
	for i := len(middleware) - 1; i >= 0; i-- {
		final = middleware[i](final)
	}
	return final
}
