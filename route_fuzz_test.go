package vial

import (
	"net/http"
	"net/http/httptest"
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

func FuzzNamedRouteURL(fuzz *testing.F) {
	for _, value := range []string{"", ".", "..", "/", "a/b", "a%2Fb", "?#", "café", "\x00"} {
		fuzz.Add(value)
	}
	app := New()
	app.Get("/items/{id}", func(c *Context) error { return c.Text(http.StatusOK, c.Param("id")) }, RouteName("item"))
	if err := app.Build(); err != nil {
		fuzz.Fatal(err)
	}
	fuzz.Fuzz(func(t *testing.T, value string) {
		if len(value) > 4<<10 {
			t.Skip()
		}
		path, err := app.URL("item", map[string]string{"id": value})
		if err != nil {
			return
		}
		request := httptest.NewRequest(http.MethodGet, path, nil)
		if request.URL.Host != "" || request.URL.RawQuery != "" || request.URL.Fragment != "" {
			t.Fatalf("parameter changed URL components: %q", path)
		}
		response := httptest.NewRecorder()
		app.ServeHTTP(response, request)
		if response.Code != http.StatusOK || response.Body.String() != value {
			t.Fatalf("parameter failed round trip: %q => %q => %d %q", value, path, response.Code, response.Body.String())
		}
	})
}
