package vial

import (
	"context"
	"errors"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

type coverageListener struct {
	closeErr error
}

func (coverageListener) Accept() (net.Conn, error) { return nil, errors.New("accept failed") }
func (listener coverageListener) Close() error     { return listener.closeErr }
func (coverageListener) Addr() net.Addr            { return coverageAddr("test") }

type coverageAddr string

func (address coverageAddr) Network() string { return "tcp" }
func (address coverageAddr) String() string  { return string(address) }

type stuckCoverageComponent struct{ done chan error }

func (component *stuckCoverageComponent) Start(context.Context) error { return nil }
func (component *stuckCoverageComponent) Done() <-chan error          { return component.done }
func (component *stuckCoverageComponent) Shutdown(context.Context) error {
	return nil
}

func invalidCoverageApp() *App {
	app := New()
	app.Handle(" ", "/", func(*Context) error { return nil })
	return app
}

func TestCoverageServerErrors(t *testing.T) {
	t.Run("route build", func(t *testing.T) {
		t.Setenv(routesOutputEnvironment, filepath.Join(t.TempDir(), "routes.json"))
		if err := invalidCoverageApp().Run(context.Background(), "127.0.0.1:0"); err == nil {
			t.Fatal("expected route build error")
		}
	})
	t.Run("route write", func(t *testing.T) {
		t.Setenv(routesOutputEnvironment, t.TempDir())
		if err := New().Run(context.Background(), "127.0.0.1:0"); err == nil || !strings.Contains(err.Error(), "write routes") {
			t.Fatalf("route write error = %v", err)
		}
	})
	t.Run("build", func(t *testing.T) {
		if err := invalidCoverageApp().Run(context.Background(), "127.0.0.1:0"); err == nil {
			t.Fatal("expected Run build error")
		}
		if err := invalidCoverageApp().Serve(context.Background(), coverageListener{}); err == nil {
			t.Fatal("expected Serve build error")
		}
		response := httptest.NewRecorder()
		invalidCoverageApp().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
		if response.Code != http.StatusInternalServerError {
			t.Fatalf("build response status = %d", response.Code)
		}
	})
	t.Run("listen", func(t *testing.T) {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer listener.Close()
		if err := New().Run(context.Background(), listener.Addr().String()); err == nil || !strings.Contains(err.Error(), "listen") {
			t.Fatalf("listen error = %v", err)
		}
	})
	t.Run("serve context and close", func(t *testing.T) {
		want := errors.New("close failed")
		err := New().Serve(nil, coverageListener{closeErr: want})
		if !errors.Is(err, want) {
			t.Fatalf("Serve() error = %v", err)
		}
	})
}

func TestCoverageBindingEdges(t *testing.T) {
	if err := validateBinding(struct{}{}); err != nil {
		t.Fatal(err)
	}
	var value string
	if err := setField(reflect.ValueOf(&value).Elem(), nil); err != nil {
		t.Fatal(err)
	}
	var number int
	if err := setScalar(reflect.ValueOf(&number).Elem(), "bad"); err == nil {
		t.Fatal("expected scalar error")
	}
	var boolean bool
	if err := setScalar(reflect.ValueOf(&boolean).Elem(), "bad"); err == nil {
		t.Fatal("expected boolean error")
	}
	if _, err := parseRequestAddress("localhost:80"); err == nil {
		t.Fatal("expected non-IP host error")
	}

	app := New()
	app.Post("/bind", func(contextValue *Context) error {
		var destination struct {
			Name string `form:"name" json:"name"`
		}
		return contextValue.Bind(&destination)
	})
	app.Post("/non-struct", func(contextValue *Context) error {
		var destination string
		return contextValue.Bind(&destination)
	})
	app.Post("/file", func(contextValue *Context) error {
		contextValue.request.MultipartForm = &multipart.Form{File: map[string][]*multipart.FileHeader{
			"file": {{Filename: "missing"}},
		}}
		_, _, err := contextValue.FormFile("file")
		return err
	})
	app.Get("/sources", func(contextValue *Context) error {
		var header struct {
			Value string `header:"X-Value"`
		}
		if err := contextValue.BindHeader(&header); err != nil {
			return err
		}
		var cookie struct {
			Value string `cookie:"value"`
		}
		return contextValue.BindCookie(&cookie)
	})

	tests := []struct {
		method, path, body, contentType string
		status                          int
	}{
		{http.MethodPost, "/bind", "{", "application/json", http.StatusBadRequest},
		{http.MethodPost, "/bind", "name=%ZZ", "application/x-www-form-urlencoded", http.StatusBadRequest},
		{http.MethodPost, "/non-struct", `"value"`, "application/json", http.StatusBadRequest},
		{http.MethodPost, "/file", "", "multipart/form-data", http.StatusBadRequest},
		{http.MethodGet, "/sources", "", "", http.StatusOK},
	}
	for _, test := range tests {
		request := httptest.NewRequest(test.method, test.path, strings.NewReader(test.body))
		request.Header.Set("Content-Type", test.contentType)
		response := httptest.NewRecorder()
		app.ServeHTTP(response, request)
		if response.Code != test.status {
			t.Fatalf("%s status = %d, want %d", test.path, response.Code, test.status)
		}
	}
}

func TestCoverageLifecycleShutdownDeadline(t *testing.T) {
	app := New(WithShutdownTimeout(time.Millisecond))
	contextValue, cancel := context.WithCancel(context.Background())
	cancel()
	err := app.runLifecycle(contextValue, &stuckCoverageComponent{done: make(chan error)})
	if err == nil || !strings.Contains(err.Error(), "shutdown deadline") {
		t.Fatalf("lifecycle error = %v", err)
	}
}

func TestCoverageEmptyTaskSupervisor(t *testing.T) {
	supervisor := newTaskSupervisor(nil, nil)
	contextValue, cancel := context.WithCancel(context.Background())
	if err := supervisor.Start(contextValue); err != nil {
		t.Fatal(err)
	}
	shutdownContext, shutdownCancel := context.WithCancel(context.Background())
	shutdownCancel()
	go func() {
		time.Sleep(time.Millisecond)
		cancel()
	}()
	if err := supervisor.Shutdown(shutdownContext); err != nil {
		t.Fatal(err)
	}
}
