package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestModuleExample(t *testing.T) {
	app, err := newApp()
	if err != nil {
		t.Fatal(err)
	}

	routes, err := app.Routes()
	if err != nil {
		t.Fatal(err)
	}
	wantModules := []string{"greetings", "greetings", "health"}
	if len(routes) != len(wantModules) {
		t.Fatalf("got %d routes, want %d: %#v", len(routes), len(wantModules), routes)
	}
	for index, want := range wantModules {
		if routes[index].Module != want {
			t.Errorf("route %d module = %q, want %q", index, routes[index].Module, want)
		}
	}

	response := httptest.NewRecorder()
	app.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
	var payload map[string]string
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusOK || payload["message"] != "Hello from a Vial module" {
		t.Fatalf("unexpected response: status=%d payload=%v", response.Code, payload)
	}

	health := httptest.NewRecorder()
	app.ServeHTTP(health, httptest.NewRequest(http.MethodGet, "/health", nil))
	if health.Code != http.StatusOK || health.Body.String() != "ok" {
		t.Fatalf("unexpected health response: status=%d body=%q", health.Code, health.Body.String())
	}

	missing := httptest.NewRecorder()
	app.ServeHTTP(missing, httptest.NewRequest(http.MethodGet, "/greetings/missing", nil))
	if missing.Code != http.StatusNotFound || !strings.Contains(missing.Body.String(), `"code":"greeting_not_found"`) {
		t.Fatalf("unexpected fault response: status=%d body=%s", missing.Code, missing.Body.String())
	}

	greeting := httptest.NewRecorder()
	app.ServeHTTP(greeting, httptest.NewRequest(http.MethodGet, "/greetings/Ada", nil))
	if greeting.Code != http.StatusOK || greeting.Body.String() != "Hello, Ada" {
		t.Fatalf("unexpected greeting: status=%d body=%q", greeting.Code, greeting.Body.String())
	}
}

func TestModulesExampleMain(t *testing.T) {
	t.Setenv("VIAL_ROUTES_OUTPUT", filepath.Join(t.TempDir(), "routes.json"))
	main()
}
