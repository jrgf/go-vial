package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jrgf/go-vial"
	"github.com/jrgf/go-vial/middleware"
)

func TestCORSActualRequests(t *testing.T) {
	cors, err := middleware.CORS(middleware.CORSConfig{
		AllowedOrigins:   []string{"https://app.example"},
		AllowedMethods:   []string{http.MethodGet},
		ExposedHeaders:   []string{"X-Trace-ID"},
		AllowCredentials: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	app := vial.New()
	app.Use(cors)
	requests := 0
	app.Get("/resource", func(context *vial.Context) error {
		requests++
		context.Response().Header().Set("X-Trace-ID", "trace-123")
		return context.NoContent(http.StatusOK)
	})

	t.Run("allowed origin", func(t *testing.T) {
		request := httptest.NewRequest(http.MethodGet, "/resource", nil)
		request.Header.Set("Origin", "https://app.example")
		response := httptest.NewRecorder()

		app.ServeHTTP(response, request)

		if response.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d", response.Code)
		}
		if got := response.Header().Get("Access-Control-Allow-Origin"); got != "https://app.example" {
			t.Fatalf("unexpected allowed origin %q", got)
		}
		if got := response.Header().Get("Access-Control-Allow-Credentials"); got != "true" {
			t.Fatalf("unexpected credentials header %q", got)
		}
		if got := response.Header().Get("Access-Control-Expose-Headers"); got != "X-Trace-ID" {
			t.Fatalf("unexpected exposed headers %q", got)
		}
		assertVary(t, response.Header(), "Origin")
	})

	t.Run("disallowed origin reaches the application without CORS headers", func(t *testing.T) {
		request := httptest.NewRequest(http.MethodGet, "/resource", nil)
		request.Header.Set("Origin", "https://other.example")
		response := httptest.NewRecorder()

		app.ServeHTTP(response, request)

		if response.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d", response.Code)
		}
		if got := response.Header().Get("Access-Control-Allow-Origin"); got != "" {
			t.Fatalf("expected no allowed origin, got %q", got)
		}
		assertVary(t, response.Header(), "Origin")
	})

	t.Run("request without origin is unchanged", func(t *testing.T) {
		response := httptest.NewRecorder()
		app.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/resource", nil))

		if response.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d", response.Code)
		}
		if got := response.Header().Get("Vary"); got != "" {
			t.Fatalf("expected no Vary header, got %q", got)
		}
	})

	if requests != 3 {
		t.Fatalf("expected application handler to run 3 times, got %d", requests)
	}
}

func TestCORSWildcardOrigin(t *testing.T) {
	cors, err := middleware.CORS(middleware.CORSConfig{AllowedOrigins: []string{"*"}})
	if err != nil {
		t.Fatal(err)
	}

	app := vial.New()
	app.Use(func(next vial.Handler) vial.Handler {
		return func(context *vial.Context) error {
			context.Response().Header().Set("Vary", "Accept-Encoding, Origin")
			return next(context)
		}
	}, cors)
	app.Get("/", func(context *vial.Context) error {
		return context.NoContent(http.StatusOK)
	})

	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set("Origin", "https://any.example")
	response := httptest.NewRecorder()
	app.ServeHTTP(response, request)

	if got := response.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("unexpected allowed origin %q", got)
	}
	if got := response.Header().Get("Access-Control-Allow-Credentials"); got != "" {
		t.Fatalf("wildcard response must not allow credentials, got %q", got)
	}
	assertVary(t, response.Header(), "Accept-Encoding", "Origin")
}

func TestCORSPreflight(t *testing.T) {
	cors, err := middleware.CORS(middleware.CORSConfig{
		AllowedOrigins:   []string{"https://app.example"},
		AllowedMethods:   []string{http.MethodGet, http.MethodPost},
		AllowedHeaders:   []string{"Content-Type", "X-API-Key"},
		AllowCredentials: true,
		MaxAge:           10 * time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}

	app := vial.New()
	app.Use(cors)
	invoked := false
	app.Post("/resource", func(context *vial.Context) error {
		invoked = true
		return context.NoContent(http.StatusCreated)
	})

	request := preflightRequest("https://app.example", http.MethodPost, "content-type, x-api-key")
	response := httptest.NewRecorder()
	app.ServeHTTP(response, request)

	if response.Code != http.StatusNoContent {
		t.Fatalf("expected status 204, got %d", response.Code)
	}
	if invoked {
		t.Fatal("preflight must not invoke the route handler")
	}
	if got := response.Header().Get("Access-Control-Allow-Origin"); got != "https://app.example" {
		t.Fatalf("unexpected allowed origin %q", got)
	}
	if got := response.Header().Get("Access-Control-Allow-Methods"); got != "GET, POST" {
		t.Fatalf("unexpected allowed methods %q", got)
	}
	if got := response.Header().Get("Access-Control-Allow-Headers"); got != "Content-Type, X-API-Key" {
		t.Fatalf("unexpected allowed headers %q", got)
	}
	if got := response.Header().Get("Access-Control-Allow-Credentials"); got != "true" {
		t.Fatalf("unexpected credentials header %q", got)
	}
	if got := response.Header().Get("Access-Control-Max-Age"); got != "600" {
		t.Fatalf("unexpected max age %q", got)
	}
	assertVary(t, response.Header(), "Origin", "Access-Control-Request-Method", "Access-Control-Request-Headers")

	withoutHeaders := httptest.NewRecorder()
	app.ServeHTTP(withoutHeaders, preflightRequest("https://app.example", http.MethodPost, ""))
	if withoutHeaders.Code != http.StatusNoContent {
		t.Fatalf("expected header-free preflight status 204, got %d", withoutHeaders.Code)
	}
}

func TestCORSDeniesInvalidPreflight(t *testing.T) {
	tests := []struct {
		name    string
		origin  string
		method  string
		headers string
	}{
		{name: "origin", origin: "https://other.example", method: http.MethodPost, headers: "X-API-Key"},
		{name: "method", origin: "https://app.example", method: http.MethodDelete, headers: "X-API-Key"},
		{name: "headers", origin: "https://app.example", method: http.MethodPost, headers: "X-Other"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cors, err := middleware.CORS(middleware.CORSConfig{
				AllowedOrigins: []string{"https://app.example"},
				AllowedMethods: []string{http.MethodPost},
				AllowedHeaders: []string{"X-API-Key"},
			})
			if err != nil {
				t.Fatal(err)
			}

			app := vial.New()
			app.Use(cors)
			invoked := false
			app.Post("/resource", func(context *vial.Context) error {
				invoked = true
				return context.NoContent(http.StatusOK)
			})

			response := httptest.NewRecorder()
			app.ServeHTTP(response, preflightRequest(test.origin, test.method, test.headers))

			if response.Code != http.StatusForbidden {
				t.Fatalf("expected status 403, got %d", response.Code)
			}
			if invoked {
				t.Fatal("denied preflight must not invoke the route handler")
			}
			if !strings.Contains(response.Body.String(), `"code":"cors_forbidden"`) {
				t.Fatalf("unexpected response body %q", response.Body.String())
			}
		})
	}
}

func TestCORSRejectsInvalidConfiguration(t *testing.T) {
	tests := []struct {
		name   string
		config middleware.CORSConfig
	}{
		{name: "wildcard with credentials", config: middleware.CORSConfig{AllowedOrigins: []string{"*"}, AllowCredentials: true}},
		{name: "wildcard with exact origin", config: middleware.CORSConfig{AllowedOrigins: []string{"*", "https://app.example"}}},
		{name: "negative max age", config: middleware.CORSConfig{MaxAge: -time.Second}},
		{name: "empty origin", config: middleware.CORSConfig{AllowedOrigins: []string{" "}}},
		{name: "invalid method", config: middleware.CORSConfig{AllowedMethods: []string{"GET\nDELETE"}}},
		{name: "invalid header", config: middleware.CORSConfig{AllowedHeaders: []string{"X-Test\rX-Other"}}},
		{name: "invalid exposed header", config: middleware.CORSConfig{ExposedHeaders: []string{"X-Test\nX-Other"}}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := middleware.CORS(test.config); err == nil {
				t.Fatal("expected configuration error")
			}
		})
	}
}

func preflightRequest(origin, method, headers string) *http.Request {
	request := httptest.NewRequest(http.MethodOptions, "/resource", nil)
	request.Header.Set("Origin", origin)
	request.Header.Set("Access-Control-Request-Method", method)
	if headers != "" {
		request.Header.Set("Access-Control-Request-Headers", headers)
	}
	return request
}

func assertVary(t *testing.T, header http.Header, expected ...string) {
	t.Helper()
	values := make(map[string]int)
	for _, line := range header.Values("Vary") {
		for _, value := range strings.Split(line, ",") {
			values[strings.TrimSpace(value)]++
		}
	}
	for _, value := range expected {
		if values[value] != 1 {
			t.Fatalf("expected Vary to contain %q once, got %q", value, header.Values("Vary"))
		}
	}
}
