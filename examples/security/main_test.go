package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSecurityExample(t *testing.T) {
	app, err := newApp()
	if err != nil {
		t.Fatal(err)
	}

	for requestNumber := 1; requestNumber <= 4; requestNumber++ {
		response := httptest.NewRecorder()
		app.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
		want := http.StatusOK
		if requestNumber == 4 {
			want = http.StatusTooManyRequests
		}
		if response.Code != want {
			t.Fatalf("request %d status = %d, want %d", requestNumber, response.Code, want)
		}
		if response.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Fatal("security headers are missing")
		}
	}
}
