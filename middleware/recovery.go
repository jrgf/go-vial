package middleware

import (
	"fmt"
	"runtime/debug"

	"github.com/jrgf/go-vial"
)

// Recover converts panics into a generic 500 error while logging the stack.
func Recover() vial.Middleware {
	return func(next vial.Handler) vial.Handler {
		return func(context *vial.Context) (err error) {
			defer func() {
				if recovered := recover(); recovered != nil {
					panicErr := fmt.Errorf("panic: %v", recovered)
					context.Logger().Error(
						"panic recovered",
						"error", panicErr,
						"stack", string(debug.Stack()),
					)
					err = vial.InternalServerError(panicErr)
				}
			}()
			return next(context)
		}
	}
}
