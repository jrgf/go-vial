package vial_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"sync"
	"testing"

	"github.com/jrgf/go-vial"
)

func TestNamedRouteURL(t *testing.T) {
	app := vial.New()
	handler := func(c *vial.Context) error {
		return c.JSON(http.StatusOK, map[string]string{"id": c.Param("id"), "path": c.Param("path")})
	}
	app.Group("/api").Group("/v1").Get("/users/{id}", handler, vial.RouteName("user"))
	app.Get("/files/{path...}", handler, vial.RouteName("files"))
	app.Get("/", handler, vial.RouteName("home"))
	app.Get("/trailing/", handler, vial.RouteName("trailing"))
	app.Get("/literal/%7Bid%7D/%25/a?b#c", handler, vial.RouteName("literal"))
	app.HandleHTTP("GET example.com/raw/{$}", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}), vial.RouteName("host"))
	tests := []struct {
		name   string
		params map[string]string
		want   string
	}{
		{"user", map[string]string{"id": "a/b ?#%"}, "/api/v1/users/a%2Fb%20%3F%23%25"},
		{"user", map[string]string{"id": "a%2Fb"}, "/api/v1/users/a%252Fb"},
		{"user", map[string]string{"id": "café"}, "/api/v1/users/caf%C3%A9"},
		{"files", map[string]string{"path": "docs/a b?.txt"}, "/files/docs/a%20b%3F.txt"},
		{"files", map[string]string{"path": ""}, "/files/"},
		{"files", map[string]string{"path": "docs/"}, "/files/docs/"},
		{"home", nil, "/"},
		{"trailing", nil, "/trailing/"},
		{"literal", nil, "/literal/%7Bid%7D/%25/a%3Fb%23c"},
		{"host", nil, "/raw/"},
	}
	for _, test := range tests {
		t.Run(test.want, func(t *testing.T) {
			path, err := app.URL(test.name, test.params)
			if err != nil || path != test.want {
				t.Fatalf("URL=%q error=%v want=%q", path, err, test.want)
			}
			parsed, err := url.Parse(path)
			if err != nil || parsed.Host != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
				t.Fatalf("generated path changed URL components: %q %v", path, err)
			}
			request := httptest.NewRequest(http.MethodGet, path, nil)
			request.Host = "example.com"
			response := httptest.NewRecorder()
			app.ServeHTTP(response, request)
			if test.name == "host" {
				if response.Code != http.StatusNoContent {
					t.Fatalf("host route returned %d", response.Code)
				}
				return
			}
			var params map[string]string
			if err := json.Unmarshal(response.Body.Bytes(), &params); err != nil || response.Code != http.StatusOK {
				t.Fatalf("generated URL did not reach its handler: %d %s %v", response.Code, response.Body.String(), err)
			}
			for key, value := range test.params {
				if params[key] != value {
					t.Errorf("parameter %s=%q want=%q", key, params[key], value)
				}
			}
		})
	}
	for _, test := range []struct {
		name   string
		params map[string]string
	}{
		{"missing", nil}, {"user", nil}, {"home", map[string]string{"extra": "x"}},
		{"user", map[string]string{"wrong": "x"}}, {"user", map[string]string{"id": ""}},
		{"user", map[string]string{"id": "."}}, {"user", map[string]string{"id": ".."}},
		{"user", map[string]string{"id": "/"}},
		{"files", map[string]string{"path": "/other.example/x"}},
		{"files", map[string]string{"path": "a//b"}}, {"files", map[string]string{"path": "a/../b"}},
	} {
		if path, err := app.URL(test.name, test.params); err == nil || path != "" {
			t.Errorf("accepted invalid URL parameters: %q %v => %q, %v", test.name, test.params, path, err)
		}
	}
	params := map[string]string{"id": "shared"}
	var workers sync.WaitGroup
	for range 8 {
		workers.Go(func() {
			for range 50 {
				if path, err := app.URL("user", params); err != nil || path != "/api/v1/users/shared" {
					t.Errorf("concurrent URL=%q error=%v", path, err)
				}
			}
		})
	}
	workers.Wait()
	if !reflect.DeepEqual(params, map[string]string{"id": "shared"}) {
		t.Fatal("URL mutated caller parameters")
	}
}

func TestNamedRouteURLValidatesRegistration(t *testing.T) {
	app := vial.New()
	app.Get("/one", func(*vial.Context) error { return nil }, vial.RouteName("duplicate"))
	app.Get("/two", func(*vial.Context) error { return nil }, vial.RouteName("duplicate"))
	if _, err := app.URL("duplicate", nil); err == nil {
		t.Fatal("URL ignored a build error")
	}
	app = vial.New()
	app.HandleHTTP("CONNECT //other.example", http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}), vial.RouteName("unsafe"))
	if path, err := app.URL("unsafe", nil); err == nil || path != "" {
		t.Fatalf("URL accepted an authority-like path: %q %v", path, err)
	}
}
