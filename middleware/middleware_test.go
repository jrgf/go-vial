package middleware_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jrgf/go-vial"
	"github.com/jrgf/go-vial/middleware"
)

func TestRequestIDUsesIncomingValue(t *testing.T) {
	app := vial.New()
	app.Use(middleware.RequestID())
	app.Get("/", func(context *vial.Context) error {
		return context.Text(http.StatusOK, middleware.RequestIDFromContext(context))
	})

	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set(middleware.RequestIDHeader, "request-123")
	response := httptest.NewRecorder()
	app.ServeHTTP(response, request)

	if response.Header().Get(middleware.RequestIDHeader) != "request-123" {
		t.Fatalf("unexpected response request ID %q", response.Header().Get(middleware.RequestIDHeader))
	}
	if response.Body.String() != "request-123" {
		t.Fatalf("unexpected body %q", response.Body.String())
	}
}

func TestRequestIDGeneratesValue(t *testing.T) {
	app := vial.New()
	app.Use(middleware.RequestID())
	app.Get("/", func(context *vial.Context) error {
		return context.Text(http.StatusOK, middleware.RequestIDFromContext(context))
	})

	response := httptest.NewRecorder()
	app.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
	requestID := response.Header().Get(middleware.RequestIDHeader)
	if len(requestID) != 32 || response.Body.String() != requestID {
		t.Fatalf("unexpected generated request ID header=%q body=%q", requestID, response.Body.String())
	}
}

func TestRecoveryReturnsGenericInternalError(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	app := vial.New(vial.WithLogger(logger))
	app.Use(middleware.Recover())
	app.Get("/panic", func(*vial.Context) error {
		panic("secret implementation detail")
	})

	response := httptest.NewRecorder()
	app.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/panic", nil))

	if response.Code != http.StatusInternalServerError {
		t.Fatalf("expected status 500, got %d", response.Code)
	}
	if strings.Contains(response.Body.String(), "secret implementation detail") {
		t.Fatalf("panic detail leaked to client: %s", response.Body.String())
	}

	var payload map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !strings.Contains(logs.String(), "secret implementation detail") {
		t.Fatal("expected panic to be present in server logs")
	}
}

func TestLoggerRecordsStatusLevels(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	app := vial.New(vial.WithLogger(logger))
	app.Use(middleware.Logger())
	app.Get("/ok", func(context *vial.Context) error {
		return context.Text(http.StatusOK, "ok")
	})
	app.Get("/bad", func(*vial.Context) error {
		return vial.BadRequest("bad", "bad request")
	})
	app.Get("/fail", func(*vial.Context) error {
		return errors.New("failed")
	})

	for _, path := range []string{"/ok", "/bad", "/fail"} {
		app.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, path, nil))
	}
	output := logs.String()
	if strings.Count(output, "HTTP request completed") != 3 {
		t.Fatalf("missing request logs:\n%s", output)
	}
	for _, level := range []string{`"level":"INFO"`, `"level":"WARN"`, `"level":"ERROR"`} {
		if !strings.Contains(output, level) {
			t.Errorf("missing %s log:\n%s", level, output)
		}
	}
}
