package middleware

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/jrgf/go-vial"
)

func TestHTTPMetricsUsesBoundedRouteLabelsAndStatuses(t *testing.T) {
	metrics := &HTTPMetrics{}
	app := vial.New(vial.WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))))
	app.Use(metrics.Middleware(), Recover())
	app.Get("/items/{id}", func(context *vial.Context) error {
		return context.NoContent(http.StatusCreated)
	})
	app.Get("/bad", func(*vial.Context) error {
		return vial.BadRequest("bad", "bad request")
	})
	app.Get("/panic", func(*vial.Context) error {
		panic("failure")
	})
	app.Get("/metrics", metrics.Handler)

	for _, path := range []string{"/items/one", "/items/two", "/bad", "/panic"} {
		app.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, path, nil))
	}
	unmatched := httptest.NewRequest("UNTRUSTED-METHOD", "/missing/attacker-value", nil)
	app.ServeHTTP(httptest.NewRecorder(), unmatched)

	response := httptest.NewRecorder()
	app.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("metrics status = %d", response.Code)
	}
	if got := response.Header().Get("Content-Type"); got != "application/openmetrics-text; version=1.0.0; charset=utf-8" {
		t.Fatalf("metrics content type = %q", got)
	}
	body := response.Body.String()
	want := []string{
		`vial_http_requests_total{method="GET",route="GET /items/{id}",status="201"} 2`,
		`vial_http_requests_total{method="GET",route="GET /bad",status="400"} 1`,
		`vial_http_requests_total{method="GET",route="GET /panic",status="500"} 1`,
		`vial_http_requests_total{method="OTHER",route="unmatched",status="404"} 1`,
		`vial_http_request_duration_seconds_count{method="GET",route="GET /items/{id}",status="201"} 2`,
		`vial_http_request_duration_seconds_bucket{method="GET",route="GET /items/{id}",status="201",le="+Inf"} 2`,
		"vial_http_requests_active 1",
		"# EOF",
	}
	for _, value := range want {
		if !strings.Contains(body, value) {
			t.Errorf("metrics missing %q:\n%s", value, body)
		}
	}
	if strings.Contains(body, "attacker-value") {
		t.Fatalf("unmatched path leaked into labels:\n%s", body)
	}
}

func TestHTTPMetricsConcurrentRecording(t *testing.T) {
	metrics := &HTTPMetrics{}
	app := vial.New()
	app.Use(metrics.Middleware())
	app.Get("/", func(context *vial.Context) error {
		return context.NoContent(http.StatusNoContent)
	})
	app.Get("/metrics", metrics.Handler)

	var wait sync.WaitGroup
	for range 100 {
		wait.Go(func() {
			app.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
		})
	}
	wait.Wait()
	response := httptest.NewRecorder()
	app.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if !strings.Contains(response.Body.String(), `vial_http_requests_total{method="GET",route="GET /{$}",status="204"} 100`) {
		t.Fatalf("unexpected concurrent metrics:\n%s", response.Body.String())
	}
}

func TestHTTPMetricsConfigurationFailures(t *testing.T) {
	var metrics *HTTPMetrics
	defer func() {
		if recover() == nil {
			t.Fatal("nil recorder did not panic")
		}
	}()
	_ = metrics.Middleware()
}
