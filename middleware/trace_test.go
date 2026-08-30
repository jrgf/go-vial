package middleware

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jrgf/go-vial"
)

const testTraceID = "4bf92f3577b34da6a3ce929d0e0e4736"

func TestTraceContextCorrelatesRequestAndLogs(t *testing.T) {
	var logs bytes.Buffer
	app := vial.New(vial.WithLogger(slog.New(slog.NewJSONHandler(&logs, nil))))
	app.Use(RequestID(), TraceContext(W3CTraceID), Logger())
	app.Get("/", func(context *vial.Context) error {
		if got := TraceIDFromContext(context); got != testTraceID {
			t.Errorf("context trace ID = %q", got)
		}
		if got := TraceIDFromRequest(context.Request()); got != testTraceID {
			t.Errorf("request trace ID = %q", got)
		}
		return context.NoContent(http.StatusNoContent)
	})

	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set(TraceParentHeader, "00-"+testTraceID+"-00f067aa0ba902b7-01")
	response := httptest.NewRecorder()
	app.ServeHTTP(response, request)

	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d", response.Code)
	}
	if !strings.Contains(logs.String(), `"trace_id":"`+testTraceID+`"`) || !strings.Contains(logs.String(), `"request_id":"`) {
		t.Fatalf("correlation fields missing from logs: %s", logs.String())
	}
}

func TestTraceContextReadsTracingMiddlewareContext(t *testing.T) {
	type contextKey struct{}
	var logs bytes.Buffer
	app := vial.New(vial.WithLogger(slog.New(slog.NewJSONHandler(&logs, nil))))
	app.UseHTTP(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			request = request.WithContext(context.WithValue(request.Context(), contextKey{}, strings.ToUpper(testTraceID)))
			next.ServeHTTP(writer, request)
		})
	})
	app.Use(TraceContext(func(request *http.Request) string {
		traceID, _ := request.Context().Value(contextKey{}).(string)
		return traceID
	}), Logger())
	app.Get("/", func(context *vial.Context) error {
		return context.NoContent(http.StatusNoContent)
	})

	app.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	if !strings.Contains(logs.String(), `"trace_id":"`+testTraceID+`"`) {
		t.Fatalf("tracer context was not correlated: %s", logs.String())
	}
}

func TestW3CTraceIDRejectsInvalidValues(t *testing.T) {
	values := []string{
		"",
		"01-" + testTraceID + "-00f067aa0ba902b7-01",
		"00-00000000000000000000000000000000-00f067aa0ba902b7-01",
		"00-" + testTraceID + "-0000000000000000-01",
		"00-" + strings.ToUpper(testTraceID) + "-00f067aa0ba902b7-01",
		"00-" + testTraceID + "-00f067aa0ba902b7-zz",
	}
	if got := W3CTraceID(nil); got != "" {
		t.Fatalf("nil request trace ID = %q", got)
	}
	for _, value := range values {
		request := httptest.NewRequest(http.MethodGet, "/", nil)
		request.Header.Set(TraceParentHeader, value)
		if got := W3CTraceID(request); got != "" {
			t.Errorf("traceparent %q produced %q", value, got)
		}
	}
}

func TestTraceContextRequiresExtractor(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("nil extractor did not panic")
		}
	}()
	_ = TraceContext(nil)
}
