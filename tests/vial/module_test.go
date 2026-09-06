package vial_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jrgf/go-vial"
	"github.com/jrgf/go-vial/testkit"
)

type testModule struct {
	name     string
	register func(*vial.Registrar) error
}

func TestModulesRegisterLifecycleAndTasks(t *testing.T) {
	var started atomic.Bool
	var stopped atomic.Bool
	taskStarted := make(chan struct{})
	module := testModule{name: "workers", register: func(registrar *vial.Registrar) error {
		registrar.OnStart(func(context.Context) error {
			started.Store(true)
			return nil
		})
		registrar.OnStop(func(context.Context) error {
			stopped.Store(true)
			return nil
		})
		registrar.Go("notifications", func(context context.Context) error {
			close(taskStarted)
			<-context.Done()
			return nil
		})
		registrar.Health("/healthz")
		registrar.Readiness("/readyz", func(context.Context) error { return nil })
		return nil
	}}

	app := vial.New()
	if err := app.Register(module); err != nil {
		t.Fatal(err)
	}
	server := testkit.Start(t, app)
	select {
	case <-taskStarted:
	case <-time.After(time.Second):
		t.Fatal("module task did not start")
	}
	if !started.Load() {
		t.Fatal("module startup hook did not run")
	}
	response, err := server.Client.Get(server.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("module health status=%d", response.StatusCode)
	}
	response, err = server.Client.Get(server.URL + "/readyz")
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("module readiness status=%d", response.StatusCode)
	}
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
	if !stopped.Load() {
		t.Fatal("module shutdown hook did not run")
	}
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
		registrar.Use(func(next vial.Handler) vial.Handler {
			return func(context *vial.Context) error {
				context.Response().Header().Set("X-Module", "health")
				return next(context)
			}
		})
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
	if healthResponse.Code != http.StatusNoContent || healthResponse.Header().Get("X-Module") != "health" {
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
