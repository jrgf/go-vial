package main

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jrgf/go-vial/middleware"
)

func TestObservabilityExample(t *testing.T) {
	var logs bytes.Buffer
	app := newApp(slog.New(slog.NewJSONHandler(&logs, nil)))
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set(middleware.RequestIDHeader, "request-123")
	request.Header.Set(middleware.TraceParentHeader, "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
	response := httptest.NewRecorder()
	app.ServeHTTP(response, request)

	if response.Code != http.StatusOK || response.Header().Get(middleware.RequestIDHeader) != "request-123" {
		t.Fatalf("response: status=%d headers=%v", response.Code, response.Header())
	}
	for _, value := range []string{"request-123", "4bf92f3577b34da6a3ce929d0e0e4736"} {
		if !strings.Contains(response.Body.String(), value) || !strings.Contains(logs.String(), value) {
			t.Fatalf("correlation value %q missing: body=%s logs=%s", value, response.Body.String(), logs.String())
		}
	}

	metrics := httptest.NewRecorder()
	app.ServeHTTP(metrics, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if metrics.Code != http.StatusOK || !strings.Contains(metrics.Body.String(), `vial_http_requests_total{method="GET",route="GET /{$}",status="200"} 1`) {
		t.Fatalf("unexpected metrics: status=%d body=%s", metrics.Code, metrics.Body.String())
	}
}
