package vial_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jrgf/go-vial"
)

func TestRouteParameterAndJSONResponse(t *testing.T) {
	app := vial.New()
	app.Get("/users/{id}", func(context *vial.Context) error {
		return context.JSON(http.StatusOK, map[string]string{
			"id": context.Param("id"),
		})
	})

	request := httptest.NewRequest(http.MethodGet, "/users/42", nil)
	response := httptest.NewRecorder()
	app.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", response.Code)
	}
	if contentType := response.Header().Get("Content-Type"); contentType != "application/json; charset=utf-8" {
		t.Fatalf("unexpected content type %q", contentType)
	}

	var body map[string]string
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body["id"] != "42" {
		t.Fatalf("expected path id 42, got %q", body["id"])
	}
}

func TestConcurrentBuildAndServeHTTP(t *testing.T) {
	app := vial.New()
	app.Get("/", func(context *vial.Context) error {
		return context.NoContent(http.StatusNoContent)
	})

	const workers = 100
	start := make(chan struct{})
	errors := make(chan string, workers)
	var group sync.WaitGroup
	group.Add(workers)
	for range workers {
		go func() {
			defer group.Done()
			<-start
			if err := app.Build(); err != nil {
				errors <- err.Error()
				return
			}
			response := httptest.NewRecorder()
			app.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
			if response.Code != http.StatusNoContent {
				errors <- http.StatusText(response.Code)
			}
		}()
	}
	close(start)
	group.Wait()
	close(errors)
	for err := range errors {
		t.Error(err)
	}
}

func TestMiddlewareOrderAcrossAppAndGroup(t *testing.T) {
	app := vial.New()
	var order []string

	makeMiddleware := func(name string) vial.Middleware {
		return func(next vial.Handler) vial.Handler {
			return func(context *vial.Context) error {
				order = append(order, name+":before")
				err := next(context)
				order = append(order, name+":after")
				return err
			}
		}
	}

	app.Use(makeMiddleware("app-a"), makeMiddleware("app-b"))
	group := app.Group("/api")
	group.Use(makeMiddleware("group"))
	group.Get("/ping", func(context *vial.Context) error {
		order = append(order, "handler")
		return context.NoContent(http.StatusNoContent)
	})

	response := httptest.NewRecorder()
	app.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/ping", nil))

	expected := []string{
		"app-a:before",
		"app-b:before",
		"group:before",
		"handler",
		"group:after",
		"app-b:after",
		"app-a:after",
	}
	if !reflect.DeepEqual(order, expected) {
		t.Fatalf("unexpected middleware order:\nwant %#v\n got %#v", expected, order)
	}
}

func TestHTTPErrorRendering(t *testing.T) {
	app := vial.New()
	app.Get("/missing", func(*vial.Context) error {
		return vial.NotFound("record_not_found", "record was not found")
	})

	response := httptest.NewRecorder()
	app.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/missing", nil))

	if response.Code != http.StatusNotFound {
		t.Fatalf("expected status 404, got %d", response.Code)
	}
	if !strings.Contains(response.Body.String(), `"code":"record_not_found"`) {
		t.Fatalf("unexpected error body %s", response.Body.String())
	}
}

func TestRoutingErrorsUseFrameworkErrorHandler(t *testing.T) {
	app := vial.New()
	app.Get("/", func(*vial.Context) error { return nil })
	app.Get("/users/{id}", func(*vial.Context) error { return nil })

	tests := []struct {
		name       string
		method     string
		path       string
		wantStatus int
		wantCode   string
		wantAllow  string
	}{
		{"not found", http.MethodGet, "/missing", http.StatusNotFound, "not_found", ""},
		{"method not allowed", http.MethodPost, "/users/42", http.StatusMethodNotAllowed, "method_not_allowed", "GET, HEAD"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			app.ServeHTTP(response, httptest.NewRequest(test.method, test.path, nil))
			if response.Code != test.wantStatus || response.Header().Get("Allow") != test.wantAllow {
				t.Fatalf("status=%d allow=%q", response.Code, response.Header().Get("Allow"))
			}
			if !strings.Contains(response.Body.String(), `"code":"`+test.wantCode+`"`) {
				t.Fatalf("unexpected error body %s", response.Body.String())
			}
		})
	}
}

func TestRoutingErrorCanBeCustomized(t *testing.T) {
	app := vial.New()
	app.Get("/users", func(*vial.Context) error { return nil })

	var handled *vial.HTTPError
	var routeWasNil bool
	app.SetErrorHandler(func(context *vial.Context, err error) {
		routeWasNil = context.Route() == nil
		if !errors.As(err, &handled) {
			t.Errorf("error is %T, want *vial.HTTPError", err)
		}
		context.Response().Header().Set("X-Custom-Error", "true")
		_ = context.Text(http.StatusTeapot, "custom")
	})

	response := httptest.NewRecorder()
	app.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/users", nil))
	if handled == nil || handled.Status != http.StatusMethodNotAllowed || handled.Headers.Get("Allow") != "GET, HEAD" {
		t.Fatalf("unexpected handled error %#v", handled)
	}
	if !routeWasNil || response.Code != http.StatusTeapot || response.Body.String() != "custom" {
		t.Fatalf("unexpected custom response: routeNil=%t status=%d body=%q", routeWasNil, response.Code, response.Body.String())
	}
	if response.Header().Get("Allow") != "GET, HEAD" || response.Header().Get("X-Custom-Error") != "true" {
		t.Fatalf("unexpected headers %v", response.Header())
	}
}

func TestRoutingPreservesServeMuxBehavior(t *testing.T) {
	app := vial.New()
	app.Get("/files/{path...}", func(context *vial.Context) error {
		return context.Text(http.StatusOK, context.Param("path"))
	})
	app.Get("/tree/", func(context *vial.Context) error {
		return context.Text(http.StatusOK, "tree")
	})
	app.HandleHTTP("GET example.com/host", http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(writer, "host")
	}))
	app.HandleHTTP("/raw", http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(writer, "raw")
	}))

	tests := []struct {
		name         string
		method       string
		target       string
		wantStatus   int
		wantBody     string
		wantLocation string
	}{
		{"wildcard", http.MethodGet, "/files/a/b", http.StatusOK, "a/b", ""},
		{"head", http.MethodHead, "/files/a/b", http.StatusOK, "", ""},
		{"redirect", http.MethodGet, "/tree", 0, "", "/tree/"},
		{"trailing slash is exact", http.MethodGet, "/tree/child", http.StatusNotFound, "", ""},
		{"host", http.MethodGet, "http://example.com/host", http.StatusOK, "host", ""},
		{"methodless raw handler", http.MethodPost, "/raw", http.StatusOK, "raw", ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			app.ServeHTTP(response, httptest.NewRequest(test.method, test.target, nil))
			statusMatches := response.Code == test.wantStatus
			if test.wantStatus == 0 {
				// Go 1.26 changed ServeMux trailing-slash redirects from 301 to 307.
				statusMatches = response.Code == http.StatusMovedPermanently || response.Code == http.StatusTemporaryRedirect
			}
			if !statusMatches || response.Header().Get("Location") != test.wantLocation {
				t.Fatalf("status=%d location=%q", response.Code, response.Header().Get("Location"))
			}
			if test.wantBody != "" && response.Body.String() != test.wantBody {
				t.Fatalf("body=%q, want %q", response.Body.String(), test.wantBody)
			}
		})
	}
}

func TestBindJSON(t *testing.T) {
	tests := []struct {
		name        string
		body        string
		contentType string
		wantStatus  int
	}{
		{name: "valid", body: `{"name":"Ada"}`, contentType: "application/json", wantStatus: http.StatusOK},
		{name: "empty body", body: ``, contentType: "application/json", wantStatus: http.StatusBadRequest},
		{name: "malformed body", body: `{"name":`, contentType: "application/json", wantStatus: http.StatusBadRequest},
		{name: "unknown field", body: `{"name":"Ada","extra":true}`, contentType: "application/json", wantStatus: http.StatusBadRequest},
		{name: "multiple values", body: `{"name":"Ada"} {"name":"Grace"}`, contentType: "application/json", wantStatus: http.StatusBadRequest},
		{name: "wrong type", body: `{"name":3}`, contentType: "application/json", wantStatus: http.StatusBadRequest},
		{name: "unsupported", body: `{"name":"Ada"}`, contentType: "text/plain", wantStatus: http.StatusUnsupportedMediaType},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			app := vial.New(vial.WithDisallowUnknownJSONFields(true))
			app.Post("/people", func(context *vial.Context) error {
				var request struct {
					Name string `json:"name"`
				}
				if err := context.BindJSON(&request); err != nil {
					return err
				}
				return context.JSON(http.StatusOK, request)
			})

			request := httptest.NewRequest(http.MethodPost, "/people", strings.NewReader(test.body))
			request.Header.Set("Content-Type", test.contentType)
			response := httptest.NewRecorder()
			app.ServeHTTP(response, request)

			if response.Code != test.wantStatus {
				t.Fatalf("expected status %d, got %d: %s", test.wantStatus, response.Code, response.Body.String())
			}
		})
	}
}

func TestBindJSONBodyLimit(t *testing.T) {
	app := vial.New(vial.WithMaxBodySize(8))
	app.Post("/body", func(context *vial.Context) error {
		var request map[string]any
		if err := context.BindJSON(&request); err != nil {
			return err
		}
		return context.NoContent(http.StatusNoContent)
	})

	request := httptest.NewRequest(http.MethodPost, "/body", bytes.NewBufferString(`{"message":"too long"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	app.ServeHTTP(response, request)

	if response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected status 413, got %d: %s", response.Code, response.Body.String())
	}
}

func TestRawHTTPHandler(t *testing.T) {
	app := vial.New()
	app.HandleHTTP("GET /raw", http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("X-Raw", "true")
		writer.WriteHeader(http.StatusAccepted)
		_, _ = io.WriteString(writer, "raw")
	}))

	response := httptest.NewRecorder()
	app.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/raw", nil))

	if response.Code != http.StatusAccepted || response.Body.String() != "raw" {
		t.Fatalf("unexpected raw response: status=%d body=%q", response.Code, response.Body.String())
	}
}

func TestRawHTTPHandlerMiddlewarePipeline(t *testing.T) {
	app := vial.New()
	var order []string
	middleware := func(name string) vial.Middleware {
		return func(next vial.Handler) vial.Handler {
			return func(context *vial.Context) error {
				if name == "app" && (context.Route() == nil || context.Route().Name != "raw") {
					t.Error("app middleware did not receive route metadata before next")
				}
				order = append(order, name+":before")
				err := next(context)
				order = append(order, name+":after")
				return err
			}
		}
	}

	app.Use(middleware("app"))
	group := app.Group("/api")
	group.Use(middleware("group"))
	group.HandleHTTP("GET /raw", http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		order = append(order, "handler")
		contextValue, ok := vial.ContextFromRequest(request)
		if !ok || contextValue.Route() == nil || contextValue.Route().Name != "raw" {
			t.Error("raw handler did not receive route metadata")
		}
	}), vial.RouteName("raw"), vial.RouteMiddleware(middleware("route")))

	response := httptest.NewRecorder()
	app.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/raw", nil))

	expected := []string{
		"app:before", "group:before", "route:before", "handler",
		"route:after", "group:after", "app:after",
	}
	if !reflect.DeepEqual(order, expected) {
		t.Fatalf("unexpected raw middleware order:\nwant %#v\n got %#v", expected, order)
	}
}

func TestRawHTTPHandlerMiddlewareCanRejectRequest(t *testing.T) {
	app := vial.New()
	called := false
	app.HandleHTTP("GET /diagnostics", http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	}), vial.RouteMiddleware(func(vial.Handler) vial.Handler {
		return func(*vial.Context) error {
			return vial.Forbidden("diagnostics_forbidden", "diagnostics access denied")
		}
	}))

	response := httptest.NewRecorder()
	app.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/diagnostics", nil))

	if called || response.Code != http.StatusForbidden {
		t.Fatalf("expected middleware rejection before raw handler, called=%v status=%d", called, response.Code)
	}
}

func TestStaticFileHandlerCanBeProtected(t *testing.T) {
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "asset.txt"), []byte("protected"), 0o600); err != nil {
		t.Fatal(err)
	}

	authenticate := func(next vial.Handler) vial.Handler {
		return func(context *vial.Context) error {
			if context.Header("Authorization") != "Bearer test" {
				return vial.Unauthorized("authentication_required", "authentication required")
			}
			return next(context)
		}
	}
	app := vial.New()
	app.HandleHTTP("GET /static/", http.StripPrefix("/static/", http.FileServer(http.Dir(directory))), vial.RouteMiddleware(authenticate))

	unauthorized := httptest.NewRecorder()
	app.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/static/asset.txt", nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("expected protected static handler to reject request, got %d", unauthorized.Code)
	}

	request := httptest.NewRequest(http.MethodGet, "/static/asset.txt", nil)
	request.Header.Set("Authorization", "Bearer test")
	authorized := httptest.NewRecorder()
	app.ServeHTTP(authorized, request)
	if authorized.Code != http.StatusOK || strings.TrimSpace(authorized.Body.String()) != "protected" {
		t.Fatalf("unexpected protected static response: status=%d body=%q", authorized.Code, authorized.Body.String())
	}
}

func TestStaticFileHandlerPreservesHeadAndRangeRequests(t *testing.T) {
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "asset.txt"), []byte("0123456789"), 0o600); err != nil {
		t.Fatal(err)
	}
	app := vial.New()
	app.HandleHTTP("GET /files/", http.StripPrefix("/files/", http.FileServer(http.Dir(directory))))

	head := httptest.NewRecorder()
	app.ServeHTTP(head, httptest.NewRequest(http.MethodHead, "/files/asset.txt", nil))
	if head.Code != http.StatusOK || head.Body.Len() != 0 || head.Header().Get("Content-Length") != "10" {
		t.Fatalf("HEAD response: status=%d length=%q body=%q", head.Code, head.Header().Get("Content-Length"), head.Body.String())
	}

	request := httptest.NewRequest(http.MethodGet, "/files/asset.txt", nil)
	request.Header.Set("Range", "bytes=2-5")
	partial := httptest.NewRecorder()
	app.ServeHTTP(partial, request)
	if partial.Code != http.StatusPartialContent || partial.Body.String() != "2345" || partial.Header().Get("Content-Range") != "bytes 2-5/10" {
		t.Fatalf("range response: status=%d range=%q body=%q", partial.Code, partial.Header().Get("Content-Range"), partial.Body.String())
	}
}

func TestBuildReportsConflictingRoutes(t *testing.T) {
	app := vial.New()
	app.Get("/users/{id}", func(*vial.Context) error { return nil })
	app.Get("/users/{name}", func(*vial.Context) error { return nil })

	if err := app.Build(); err == nil {
		t.Fatal("expected route conflict")
	}
}

func TestBuildRejectsInvalidRouteMethod(t *testing.T) {
	app := vial.New()
	app.Handle(" ", "/items", func(*vial.Context) error { return nil })
	if err := app.Build(); err == nil || !strings.Contains(err.Error(), "invalid method") {
		t.Fatalf("invalid route method returned %v", err)
	}
}

func TestBuildValidatesRouteNames(t *testing.T) {
	tests := []struct {
		name      string
		register  func(*vial.App)
		wantError string
	}{
		{
			name: "duplicate",
			register: func(app *vial.App) {
				app.Get("/one", func(*vial.Context) error { return nil }, vial.RouteName("shared"))
				app.Post("/two", func(*vial.Context) error { return nil }, vial.RouteName("shared"))
			},
			wantError: "duplicate route name",
		},
		{
			name: "empty",
			register: func(app *vial.App) {
				app.Get("/one", func(*vial.Context) error { return nil }, vial.RouteName(""))
			},
			wantError: "invalid name",
		},
		{
			name: "whitespace",
			register: func(app *vial.App) {
				app.Get("/one", func(*vial.Context) error { return nil }, vial.RouteName(" bad\nname"))
			},
			wantError: "invalid name",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			app := vial.New()
			test.register(app)
			if err := app.Build(); err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("Build() error = %v, want %q", err, test.wantError)
			}
		})
	}
}

func TestRoutesReturnsReadOnlyRegistrationOrder(t *testing.T) {
	app := vial.New()
	app.Use(func(next vial.Handler) vial.Handler { return next })
	app.Get("/users/{id}", func(*vial.Context) error { return nil }, vial.RouteName("users.show"))
	app.Group("/api").Post("notes", func(*vial.Context) error { return nil }, vial.RouteName("notes.create"), vial.RouteMiddleware(func(next vial.Handler) vial.Handler { return next }))
	app.HandleHTTP("GET /health/{region}", http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}), vial.RouteName("health"))

	routes, err := app.Routes()
	if err != nil {
		t.Fatalf("routes: %v", err)
	}
	want := []vial.Route{
		{Method: http.MethodGet, Path: "/users/{id}", Pattern: "GET /users/{id}", Name: "users.show", MiddlewareCount: 1, Parameters: []string{"id"}},
		{Method: http.MethodPost, Path: "/api/notes", Pattern: "POST /api/notes", Name: "notes.create", MiddlewareCount: 2},
		{Method: http.MethodGet, Path: "/health/{region}", Pattern: "GET /health/{region}", Name: "health", MiddlewareCount: 1, Parameters: []string{"region"}},
	}
	if !reflect.DeepEqual(routes, want) {
		t.Fatalf("routes = %#v, want %#v", routes, want)
	}

	routes[0].Path = "/changed"
	routes[0].Parameters[0] = "changed"
	again, err := app.Routes()
	if err != nil {
		t.Fatalf("routes again: %v", err)
	}
	if !reflect.DeepEqual(again, want) {
		t.Fatalf("mutating returned routes changed app routes: %#v", again)
	}
}

func TestRoutesReportsBuildErrors(t *testing.T) {
	app := vial.New()
	app.Get("/users/{id}", func(*vial.Context) error { return nil })
	app.Get("/users/{name}", func(*vial.Context) error { return nil })

	if _, err := app.Routes(); err == nil {
		t.Fatal("expected route conflict")
	}
}

func TestRunWritesRouteInspectionWithoutListening(t *testing.T) {
	output := filepath.Join(t.TempDir(), "routes.json")
	t.Setenv("VIAL_ROUTES_OUTPUT", output)

	app := vial.New()
	app.Get("/health", func(*vial.Context) error { return nil })
	if err := app.Run(context.Background(), "not a valid address"); err != nil {
		t.Fatalf("inspect routes: %v", err)
	}

	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatalf("read routes: %v", err)
	}
	var routes []vial.Route
	if err := json.Unmarshal(data, &routes); err != nil {
		t.Fatalf("decode routes: %v", err)
	}
	want := []vial.Route{{Method: http.MethodGet, Path: "/health", Pattern: "GET /health"}}
	if !reflect.DeepEqual(routes, want) {
		t.Fatalf("routes = %#v, want %#v", routes, want)
	}
}

func TestApplicationIsImmutableAfterBuild(t *testing.T) {
	app := vial.New()
	if err := app.Build(); err != nil {
		t.Fatalf("build: %v", err)
	}

	defer func() {
		if recover() == nil {
			t.Fatal("expected route registration to panic after build")
		}
	}()
	app.Get("/late", func(*vial.Context) error { return nil })
}

func TestGroupMiddlewareIsImmutableAfterBuild(t *testing.T) {
	app := vial.New()
	group := app.Group("/api")
	if err := app.Build(); err != nil {
		t.Fatalf("build: %v", err)
	}

	defer func() {
		if recover() == nil {
			t.Fatal("expected group middleware registration to panic after build")
		}
	}()
	group.Use(func(next vial.Handler) vial.Handler { return next })
}

func TestServeStopsWhenContextIsCanceled(t *testing.T) {
	app := vial.New(vial.WithShutdownTimeout(time.Second))
	app.Get("/", func(context *vial.Context) error {
		return context.Text(http.StatusOK, "ok")
	})

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	serveContext, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		result <- app.Serve(serveContext, listener)
	}()

	client := &http.Client{Timeout: time.Second}
	deadline := time.Now().Add(2 * time.Second)
	for {
		response, requestErr := client.Get("http://" + listener.Addr().String() + "/")
		if requestErr == nil {
			_ = response.Body.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("server did not become ready: %v", requestErr)
		}
		time.Sleep(10 * time.Millisecond)
	}

	cancel()
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("serve returned an error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server did not stop after context cancellation")
	}
}

func TestServeCancelsActiveRequestsWhenContextIsCanceled(t *testing.T) {
	app := vial.New(vial.WithShutdownTimeout(time.Second))
	started := make(chan struct{})
	stopped := make(chan struct{})
	app.Get("/", func(contextValue *vial.Context) error {
		close(started)
		<-contextValue.Request().Context().Done()
		close(stopped)
		return nil
	})

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	serveContext, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- app.Serve(serveContext, listener) }()

	requestDone := make(chan struct{})
	go func() {
		response, requestErr := http.Get("http://" + listener.Addr().String() + "/")
		if requestErr == nil {
			_ = response.Body.Close()
		}
		close(requestDone)
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("request did not start")
	}

	cancel()
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("active request context was not canceled")
	}
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("serve returned an error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server did not stop")
	}
	select {
	case <-requestDone:
	case <-time.After(2 * time.Second):
		t.Fatal("request did not finish")
	}
}

func TestMultipleRoutesKeepTheirOwnHandlers(t *testing.T) {
	app := vial.New()
	app.Get("/one", func(context *vial.Context) error {
		return context.Text(http.StatusOK, "one")
	})
	app.Get("/two", func(context *vial.Context) error {
		return context.Text(http.StatusOK, "two")
	})

	for path, expected := range map[string]string{"/one": "one", "/two": "two"} {
		response := httptest.NewRecorder()
		app.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Body.String() != expected {
			t.Fatalf("route %s returned %q, want %q", path, response.Body.String(), expected)
		}
	}
}

func TestJSONEncodingFailureCanStillRenderAnError(t *testing.T) {
	app := vial.New()
	app.Get("/invalid", func(context *vial.Context) error {
		return context.JSON(http.StatusOK, make(chan int))
	})

	response := httptest.NewRecorder()
	app.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/invalid", nil))
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("expected status 500, got %d: %s", response.Code, response.Body.String())
	}
}

func TestGroupEmptyPathMatchesPrefixWithoutTrailingSlash(t *testing.T) {
	app := vial.New()
	group := app.Group("/api")
	group.Get("", func(context *vial.Context) error {
		return context.Text(http.StatusOK, "api root")
	})

	response := httptest.NewRecorder()
	app.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api", nil))
	if response.Code != http.StatusOK || response.Body.String() != "api root" {
		t.Fatalf("unexpected response: status=%d body=%q", response.Code, response.Body.String())
	}
}

func TestPublicHTTPHelpersAndContextAccessors(t *testing.T) {
	app := vial.New()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := func(context *vial.Context) error {
		if context.App() != app || context.Request() == nil || context.Response() == nil || context.Route() == nil {
			t.Error("context accessors did not return request state")
		}
		if context.Query("query") != "value" || context.Header("X-Test") != "header" {
			t.Error("request accessors returned unexpected values")
		}
		context.SetLogger(logger)
		if context.Logger() != logger {
			t.Error("logger was not updated")
		}
		context.Set("method", context.Request().Method)
		if value, ok := context.Get("method"); !ok || value != context.Request().Method {
			t.Errorf("unexpected context value %q", value)
		}
		if err := context.Text(http.StatusAccepted, context.Request().Method); err != nil {
			return err
		}
		if context.Status() != http.StatusAccepted || context.BytesWritten() == 0 || !context.Committed() {
			t.Error("response accessors did not report the written response")
		}
		return nil
	}

	registrations := []struct {
		method   string
		register func(string, vial.Handler, ...vial.RouteOption)
	}{
		{http.MethodPut, app.Put},
		{http.MethodPatch, app.Patch},
		{http.MethodDelete, app.Delete},
		{http.MethodOptions, app.Options},
	}
	for _, registration := range registrations {
		registration.register("/resource", handler)
	}
	for _, registration := range registrations {
		request := httptest.NewRequest(registration.method, "/resource?query=value", nil)
		request.Header.Set("X-Test", "header")
		response := httptest.NewRecorder()
		app.ServeHTTP(response, request)
		if response.Code != http.StatusAccepted || response.Body.String() != registration.method {
			t.Errorf("%s response: status=%d body=%q", registration.method, response.Code, response.Body.String())
		}
	}
}

func TestTypedRequestValuesAreCollisionSafeAndAvailableToNetHTTP(t *testing.T) {
	first := vial.NewValueKey[string]("shared")
	second := vial.NewValueKey[int]("shared")
	app := vial.New()
	app.HandleHTTP("GET /raw", http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		firstValue, firstOK := first.FromRequest(request)
		secondValue, secondOK := second.FromRequest(request)
		if !firstOK || firstValue != "one" || !secondOK || secondValue != 2 {
			t.Errorf("unexpected request values: %q/%v %d/%v", firstValue, firstOK, secondValue, secondOK)
		}
		writer.WriteHeader(http.StatusNoContent)
	}), vial.RouteMiddleware(func(next vial.Handler) vial.Handler {
		return func(context *vial.Context) error {
			first.Set(context, "one")
			second.Set(context, 2)
			if value, ok := first.Get(context); !ok || value != "one" {
				t.Errorf("unexpected Vial context value %q/%v", value, ok)
			}
			return next(context)
		}
	}))

	response := httptest.NewRecorder()
	app.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/raw", nil))
	if response.Code != http.StatusNoContent {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestNestedGroupHTTPHelpers(t *testing.T) {
	app := vial.New()
	group := app.Group("/api").Group("/v1")
	registrations := []struct {
		method   string
		path     string
		register func(string, vial.Handler, ...vial.RouteOption)
	}{
		{http.MethodPost, "/post", group.Post},
		{http.MethodPut, "/put", group.Put},
		{http.MethodPatch, "/patch", group.Patch},
		{http.MethodDelete, "/delete", group.Delete},
		{http.MethodOptions, "/options", group.Options},
	}
	for _, registration := range registrations {
		registration.register(registration.path, func(context *vial.Context) error {
			return context.NoContent(http.StatusNoContent)
		})
	}
	for _, registration := range registrations {
		response := httptest.NewRecorder()
		app.ServeHTTP(response, httptest.NewRequest(registration.method, "/api/v1"+registration.path, nil))
		if response.Code != http.StatusNoContent {
			t.Errorf("%s %s returned %d", registration.method, registration.path, response.Code)
		}
	}
}

func TestRedirectAndCustomErrorHandler(t *testing.T) {
	app := vial.New()
	app.Get("/redirect", func(context *vial.Context) error {
		return context.Redirect(http.StatusFound, "/target")
	})
	app.Get("/error", func(*vial.Context) error {
		return errors.New("boom")
	})
	app.SetErrorHandler(func(context *vial.Context, _ error) {
		_ = context.Text(http.StatusTeapot, "handled")
	})

	redirect := httptest.NewRecorder()
	app.ServeHTTP(redirect, httptest.NewRequest(http.MethodGet, "/redirect", nil))
	if redirect.Code != http.StatusFound || redirect.Header().Get("Location") != "/target" {
		t.Fatalf("unexpected redirect: status=%d location=%q", redirect.Code, redirect.Header().Get("Location"))
	}

	response := httptest.NewRecorder()
	app.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/error", nil))
	if response.Code != http.StatusTeapot || response.Body.String() != "handled" {
		t.Fatalf("unexpected custom error response: status=%d body=%q", response.Code, response.Body.String())
	}
}

func TestCustomErrorHandlerPanicUsesFrameworkFallback(t *testing.T) {
	var logs bytes.Buffer
	app := vial.New(vial.WithLogger(slog.New(slog.NewTextHandler(&logs, nil))))
	app.Get("/error", func(*vial.Context) error {
		return errors.New("request exploded")
	})
	calls := 0
	app.SetErrorHandler(func(context *vial.Context, _ error) {
		calls++
		context.Response().Header().Set("X-Internal-Detail", "secret")
		panic("renderer exploded")
	})

	response := httptest.NewRecorder()
	app.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/error", nil))

	if calls != 1 || response.Code != http.StatusInternalServerError || response.Body.String() != "Internal Server Error\n" {
		t.Fatalf("unexpected fallback: calls=%d status=%d body=%q", calls, response.Code, response.Body.String())
	}
	if response.Header().Get("X-Internal-Detail") != "" || strings.Contains(response.Body.String(), "exploded") {
		t.Fatal("framework fallback exposed custom renderer details")
	}
	if output := logs.String(); !strings.Contains(output, "request exploded") || !strings.Contains(output, "renderer exploded") {
		t.Fatalf("panic log does not contain both failures: %s", output)
	}
}

func TestCustomErrorHandlerPanicAfterCommitDoesNotEscape(t *testing.T) {
	app := vial.New()
	app.Get("/error", func(*vial.Context) error { return errors.New("boom") })
	app.SetErrorHandler(func(context *vial.Context, _ error) {
		_ = context.Text(http.StatusTeapot, "partial")
		panic("too late")
	})

	response := httptest.NewRecorder()
	app.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/error", nil))
	if response.Code != http.StatusTeapot || response.Body.String() != "partial" {
		t.Fatalf("committed response changed after renderer panic: status=%d body=%q", response.Code, response.Body.String())
	}
}

func TestHTTPErrorHelpers(t *testing.T) {
	cause := errors.New("database unavailable")
	wrapped := vial.WrapHTTPError(http.StatusConflict, "conflict", "write conflict", cause)
	if !errors.Is(wrapped, cause) || !strings.Contains(wrapped.Error(), cause.Error()) {
		t.Fatal("wrapped HTTP error did not retain its cause")
	}
	if (*vial.HTTPError)(nil).Error() != "<nil>" || (*vial.HTTPError)(nil).Unwrap() != nil {
		t.Fatal("nil HTTP error methods returned unexpected values")
	}

	errorsByStatus := []error{
		vial.Unauthorized("unauthorized", "unauthorized"),
		vial.Forbidden("forbidden", "forbidden"),
		vial.MethodNotAllowed("method_not_allowed", "method not allowed"),
		vial.Conflict("conflict", "conflict"),
		vial.InternalServerError(cause),
	}
	wantStatuses := []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusMethodNotAllowed, http.StatusConflict, http.StatusInternalServerError}
	for index, err := range errorsByStatus {
		if got := vial.StatusCode(err); got != wantStatuses[index] {
			t.Errorf("StatusCode(%v) = %d, want %d", err, got, wantStatuses[index])
		}
	}
	if got := vial.StatusCode(errors.New("plain")); got != http.StatusInternalServerError {
		t.Fatalf("plain error status = %d", got)
	}
	if got := vial.StatusCode(vial.NewHTTPError(http.StatusOK, "invalid", "invalid")); got != http.StatusInternalServerError {
		t.Fatalf("invalid HTTP error status = %d", got)
	}
}

func TestRunStopsWhenContextIsAlreadyCanceled(t *testing.T) {
	app := vial.New()
	contextValue, cancel := context.WithCancel(context.Background())
	cancel()
	if err := app.Run(contextValue, "127.0.0.1:0"); err != nil {
		t.Fatalf("run: %v", err)
	}
}
