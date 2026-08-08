package middleware

import (
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/jrgf/go-vial"
)

// RequestIDHeader is the HTTP header used to carry request IDs.
const RequestIDHeader = "X-Request-ID"

// RequestIDKey is the collision-safe request ID value key.
var RequestIDKey = vial.NewValueKey[string]("request_id")

var fallbackRequestID atomic.Uint64

// RequestID accepts a reasonable incoming request ID or generates a new one.
func RequestID() vial.Middleware {
	return func(next vial.Handler) vial.Handler {
		return func(context *vial.Context) error {
			requestID := strings.TrimSpace(context.Request().Header.Get(RequestIDHeader))
			if requestID == "" || len(requestID) > 128 || strings.ContainsAny(requestID, "\r\n") {
				requestID = newRequestID()
			}

			RequestIDKey.Set(context, requestID)
			context.SetLogger(context.Logger().With("request_id", requestID))
			context.Response().Header().Set(RequestIDHeader, requestID)
			return next(context)
		}
	}
}

// RequestIDFromContext returns the request ID installed by RequestID.
func RequestIDFromContext(context *vial.Context) string {
	requestID, _ := RequestIDKey.Get(context)
	return requestID
}

// RequestIDFromRequest returns the request ID through standard net/http.
func RequestIDFromRequest(request *http.Request) string {
	requestID, _ := RequestIDKey.FromRequest(request)
	return requestID
}

func newRequestID() string {
	var random [16]byte
	if _, err := randomRead(random[:]); err == nil {
		return hex.EncodeToString(random[:])
	}

	sequence := fallbackRequestID.Add(1)
	return fmt.Sprintf("%x-%x", time.Now().UnixNano(), sequence)
}
