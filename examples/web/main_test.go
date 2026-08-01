package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWebExample(t *testing.T) {
	app, err := newApp()
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		path        string
		contentType string
		body        string
	}{
		{"/", "text/html; charset=utf-8", "Hello, &lt;Vial&gt;"},
		{"/static/app.css", "text/css; charset=utf-8", "font-family"},
	}
	for _, test := range tests {
		t.Run(test.path, func(t *testing.T) {
			response := httptest.NewRecorder()
			app.ServeHTTP(response, httptest.NewRequest(http.MethodGet, test.path, nil))
			if response.Code != http.StatusOK {
				t.Fatalf("status=%d", response.Code)
			}
			if got := response.Header().Get("Content-Type"); got != test.contentType {
				t.Fatalf("content type=%q", got)
			}
			if !strings.Contains(response.Body.String(), test.body) {
				t.Fatalf("body=%q", response.Body.String())
			}
		})
	}
}
