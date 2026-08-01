package vial_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jrgf/go-vial"
)

type testModule struct {
	name     string
	register func(*vial.Registrar) error
}

func (module testModule) Name() string {
	return module.name
}

func (module testModule) Register(registrar *vial.Registrar) error {
	if module.register == nil {
		return nil
	}
	return module.register(registrar)
}

func TestModulesRegisterRoutesAndMiddleware(t *testing.T) {
	users := testModule{name: "users", register: func(registrar *vial.Registrar) error {
		registrar.Use(func(next vial.Handler) vial.Handler {
			return func(context *vial.Context) error {
				context.Response().Header().Set("X-Module", "users")
				return next(context)
			}
		})
		registrar.Group("/users").Get("/{id}", func(context *vial.Context) error {
			return context.Text(http.StatusOK, context.Param("id"))
		}, vial.RouteName("users.show"))
		return nil
	}}
	health := testModule{name: "health", register: func(registrar *vial.Registrar) error {
		registrar.HandleHTTP("GET /health", http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			writer.WriteHeader(http.StatusNoContent)
		}), vial.RouteName("health"))
		return nil
	}}

	app := vial.New()
	if err := app.Register(users, health); err != nil {
		t.Fatal(err)
	}
	routes, err := app.Routes()
	if err != nil {
		t.Fatal(err)
	}
	if len(routes) != 2 || routes[0].Name != "users.show" || routes[0].Module != "users" || routes[1].Module != "health" {
		t.Fatalf("unexpected module routes: %#v", routes)
	}

	usersResponse := httptest.NewRecorder()
	app.ServeHTTP(usersResponse, httptest.NewRequest(http.MethodGet, "/users/42", nil))
	if usersResponse.Code != http.StatusOK || usersResponse.Body.String() != "42" || usersResponse.Header().Get("X-Module") != "users" {
		t.Fatalf("unexpected users response: status=%d body=%q headers=%v", usersResponse.Code, usersResponse.Body.String(), usersResponse.Header())
	}

	healthResponse := httptest.NewRecorder()
	app.ServeHTTP(healthResponse, httptest.NewRequest(http.MethodGet, "/health", nil))
	if healthResponse.Code != http.StatusNoContent || healthResponse.Header().Get("X-Module") != "" {
		t.Fatalf("unexpected health response: status=%d headers=%v", healthResponse.Code, healthResponse.Header())
	}
}

func TestBuildValidatesModuleNames(t *testing.T) {
	tests := []struct {
		name      string
		modules   []vial.Module
		wantError string
	}{
		{name: "duplicate", modules: []vial.Module{testModule{name: "users"}, testModule{name: "users"}}, wantError: "duplicate module name"},
		{name: "empty", modules: []vial.Module{testModule{name: ""}}, wantError: "invalid module name"},
		{name: "whitespace", modules: []vial.Module{testModule{name: " bad\nname"}}, wantError: "invalid module name"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			app := vial.New()
			if err := app.Register(test.modules...); err != nil {
				t.Fatal(err)
			}
			if err := app.Build(); err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("Build() error = %v, want %q", err, test.wantError)
			}
		})
	}
}

func TestModuleRegistrationIsAtomic(t *testing.T) {
	wantErr := errors.New("module failed")
	good := testModule{name: "good", register: func(registrar *vial.Registrar) error {
		registrar.Handle(http.MethodGet, "/good", func(*vial.Context) error { return nil })
		return nil
	}}
	bad := testModule{name: "bad", register: func(*vial.Registrar) error {
		return wantErr
	}}

	app := vial.New()
	if err := app.Register(good, bad); !errors.Is(err, wantErr) {
		t.Fatalf("Register() error = %v, want %v", err, wantErr)
	}
	routes, err := app.Routes()
	if err != nil {
		t.Fatal(err)
	}
	if len(routes) != 0 {
		t.Fatalf("failed registration committed routes: %#v", routes)
	}
}

func TestRegisterRejectsNilModuleAndBuiltApp(t *testing.T) {
	app := vial.New()
	if err := app.Register(nil); err == nil {
		t.Fatal("expected nil module error")
	}
	if err := app.Build(); err != nil {
		t.Fatal(err)
	}
	if err := app.Register(testModule{name: "late"}); err == nil {
		t.Fatal("expected built application error")
	}
}
