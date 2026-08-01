package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestExampleRoutes(t *testing.T) {
	app := newApp()
	tests := []struct {
		path string
		key  string
		want string
	}{
		{"/", "message", "Hello from vial"},
		{"/users/42", "id", "42"},
		{"/search?q=hello+vial", "query", "hello vial"},
		{"/search", "query", ""},
	}
	for _, test := range tests {
		response := httptest.NewRecorder()
		app.ServeHTTP(response, httptest.NewRequest(http.MethodGet, test.path, nil))
		var payload map[string]string
		if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
			t.Fatalf("decode %s: %v", test.path, err)
		}
		if response.Code != http.StatusOK || payload[test.key] != test.want {
			t.Errorf("%s: status=%d payload=%v", test.path, response.Code, payload)
		}
	}
}

func TestExampleUnknownRoute(t *testing.T) {
	response := httptest.NewRecorder()
	newApp().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/missing", nil))
	if response.Code != http.StatusNotFound || !strings.Contains(response.Body.String(), `"code":"not_found"`) {
		t.Fatalf("unexpected missing route response: status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestExampleMethodNotAllowed(t *testing.T) {
	response := httptest.NewRecorder()
	newApp().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/submit", nil))
	if response.Code != http.StatusMethodNotAllowed || response.Header().Get("Allow") != http.MethodPost {
		t.Fatalf("unexpected method response: status=%d allow=%q", response.Code, response.Header().Get("Allow"))
	}
	if !strings.Contains(response.Body.String(), `"code":"method_not_allowed"`) {
		t.Fatalf("unexpected method response body: %s", response.Body.String())
	}
}
