package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
