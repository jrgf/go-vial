package vial

import (
	"net/http"
	"testing"
)

func FuzzRoutePatternRegistration(fuzz *testing.F) {
	fuzz.Add("GET /users/{id}", "GET", "/users/{id}")
	fuzz.Add("bad pattern", "bad method", "{")
	fuzz.Add("/", "", "/")
	fuzz.Fuzz(func(t *testing.T, rawPattern, method, path string) {
		if len(rawPattern)+len(method)+len(path) > 4<<10 {
			t.Skip()
		}
		raw := New()
		raw.HandleHTTP(rawPattern, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
		_ = raw.Build()

		framework := New()
		framework.Handle(method, path, func(*Context) error { return nil })
		_ = framework.Build()
	})
}
