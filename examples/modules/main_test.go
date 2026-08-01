package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
	if len(routes) != 3 || routes[0].Module != "greetings" || routes[1].Module != "greetings" || routes[2].Module != "health" {
		t.Fatalf("unexpected routes: %#v", routes)
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
}
