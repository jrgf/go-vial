package vial

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCachedRouteMatchInvalidatesOnRewrite(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /before", func(http.ResponseWriter, *http.Request) {})
	mux.HandleFunc("POST /after/{id}", func(http.ResponseWriter, *http.Request) {})
	r := httptest.NewRequest("GET", "/before", nil)
	var match muxMatch
	if _, pattern := match.resolve(mux, r); pattern != "GET /before" {
		t.Fatal(pattern)
	}
	r.Method, r.URL.Path = "POST", "/after/42"
	if _, pattern := match.resolve(mux, r); pattern != "POST /after/{id}" {
		t.Fatal(pattern)
	}
	r.Method = "DELETE"
	handler, pattern := match.resolve(mux, r)
	if err := routeMiss(handler, pattern, r); err == nil || err.Status != 405 {
		t.Fatalf("error=%v", err)
	}
}
