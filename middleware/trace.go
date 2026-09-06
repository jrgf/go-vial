package middleware

import (
	"net/http"
	"strings"

	"github.com/jrgf/go-vial"
)

// TraceParentHeader is the W3C Trace Context propagation header.
const TraceParentHeader = "traceparent"

var traceIDKey = vial.NewValueKey[string]("trace_id")

// TraceContext adds a validated trace ID to request state and slog records.
// The extractor may read a tracing library's request context or use W3CTraceID.
func TraceContext(extract func(*http.Request) string) vial.Middleware {
	if extract == nil {
		panic("vial: trace ID extractor is required")
	}
	return func(next vial.Handler) vial.Handler {
		return func(context *vial.Context) error {
			traceID := normalizeTraceID(extract(context.Request()))
			if traceID != "" {
				traceIDKey.Set(context, traceID)
				context.SetLogger(context.Logger().With("trace_id", traceID))
			}
			return next(context)
		}
	}
}

// W3CTraceID returns the trace ID from a valid version 00 traceparent header.
func W3CTraceID(request *http.Request) string {
	if request == nil {
		return ""
	}
	value := request.Header.Get(TraceParentHeader)
	if len(value) != 55 || value[2] != '-' || value[35] != '-' || value[52] != '-' || value[:2] != "00" {
		return ""
	}
	traceID := value[3:35]
	parentID := value[36:52]
	if !validLowerHex(traceID) || allZero(traceID) || !validLowerHex(parentID) || allZero(parentID) || !validLowerHex(value[53:]) {
		return ""
	}
	return traceID
}

// TraceIDFromContext returns the trace ID installed by TraceContext.
func TraceIDFromContext(context *vial.Context) string {
	traceID, _ := traceIDKey.Get(context)
	return traceID
}

// TraceIDFromRequest returns the same trace ID through standard net/http.
func TraceIDFromRequest(request *http.Request) string {
	traceID, _ := traceIDKey.FromRequest(request)
	return traceID
}

func normalizeTraceID(value string) string {
	if len(value) != 32 {
		return ""
	}
	value = strings.ToLower(value)
	if !validLowerHex(value) || allZero(value) {
		return ""
	}
	return value
}

func validLowerHex(value string) bool {
	for index := range len(value) {
		character := value[index]
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func allZero(value string) bool {
	return strings.Trim(value, "0") == ""
}
