package middleware

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/jrgf/go-vial"
)

const (
	RequestIDHeader = "X-Request-ID"
	RequestIDKey    = "request_id"
)

var fallbackRequestID atomic.Uint64

// RequestID accepts a reasonable incoming request ID or generates a new one.
func RequestID() vial.Middleware {
	return func(next vial.Handler) vial.Handler {
		return func(context *vial.Context) error {
			requestID := strings.TrimSpace(context.Request().Header.Get(RequestIDHeader))
			if requestID == "" || len(requestID) > 128 || strings.ContainsAny(requestID, "\r\n") {
				requestID = newRequestID()
			}

			context.Set(RequestIDKey, requestID)
			context.SetLogger(context.Logger().With("request_id", requestID))
			context.Response().Header().Set(RequestIDHeader, requestID)
			return next(context)
		}
	}
}

func RequestIDFromContext(context *vial.Context) string {
	value, ok := context.Get(RequestIDKey)
	if !ok {
		return ""
	}
	requestID, _ := value.(string)
	return requestID
}

func newRequestID() string {
	var random [16]byte
	if _, err := rand.Read(random[:]); err == nil {
		return hex.EncodeToString(random[:])
	}

	sequence := fallbackRequestID.Add(1)
	return fmt.Sprintf("%x-%x", time.Now().UnixNano(), sequence)
}
