package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNamedLocationAndProblemDetails(t *testing.T) {
	app, err := newApp()
	if err != nil {
		t.Fatal(err)
	}
	for _, title := range []string{"Release checklist", ""} {
		request := httptest.NewRequest(http.MethodPost, "/notes", strings.NewReader(`{"title":"`+title+`"}`))
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		app.ServeHTTP(response, request)
		if title != "" {
			if response.Code != http.StatusCreated || response.Header().Get("Location") != "/notes/1" {
				t.Fatalf("create response: %d %v", response.Code, response.Header())
			}
			continue
		}
		var problem struct{ Type, Code string }
		if err := json.Unmarshal(response.Body.Bytes(), &problem); err != nil {
			t.Fatal(err)
		}
		if response.Code != http.StatusBadRequest || response.Header().Get("Content-Type") != "application/problem+json" || problem.Type != "about:blank" || problem.Code != "title_required" {
			t.Fatalf("invalid note response: %d %s", response.Code, response.Body.String())
		}
	}
}
