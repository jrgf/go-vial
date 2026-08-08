package vial

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestContextEdgeContracts(t *testing.T) {
	key := NewValueKey[int]("count")
	if key.Name() != "count" {
		t.Fatalf("key name = %q", key.Name())
	}
	if _, ok := key.FromRequest(nil); ok {
		t.Fatal("nil request contained a value")
	}
	plainRequest := httptest.NewRequest(http.MethodGet, "/", nil)
	if _, ok := key.FromRequest(plainRequest); ok {
		t.Fatal("plain request contained a value")
	}
	if _, ok := requestValue[int](nil, key); ok {
		t.Fatal("nil values contained a value")
	}
	values := &requestValues{values: make(map[any]any)}
	if _, ok := requestValue[int](values, nil); ok {
		t.Fatal("nil key returned a value")
	}
	if _, ok := requestValue[int](values, key); ok {
		t.Fatal("missing key returned a value")
	}
	values.set(key, "wrong type")
	if _, ok := requestValue[int](values, key); ok {
		t.Fatal("wrong value type was accepted")
	}
	if _, ok := ContextFromRequest(nil); ok {
		t.Fatal("nil request returned a context")
	}
	if _, ok := ContextFromRequest(plainRequest); ok {
		t.Fatal("plain request returned a context")
	}

	recorder := httptest.NewRecorder()
	contextValue := newContext(New(), newResponseWriter(recorder), plainRequest)
	contextValue.logger = nil
	if contextValue.Logger() != slog.Default() {
		t.Fatal("nil logger did not fall back to slog.Default")
	}
	contextValue.response.WriteHeader(http.StatusAccepted)
	for name, write := range map[string]func() error{
		"json":       func() error { return contextValue.JSON(http.StatusOK, map[string]string{}) },
		"text":       func() error { return contextValue.Text(http.StatusOK, "text") },
		"no content": func() error { return contextValue.NoContent(http.StatusNoContent) },
		"redirect":   func() error { return contextValue.Redirect(http.StatusFound, "/next") },
	} {
		if err := write(); err == nil {
			t.Errorf("committed %s response succeeded", name)
		}
	}
	defaultErrorHandler(contextValue, errors.New("after commit"))

	for _, destination := range []any{nil, struct{}{}} {
		contextValue := newContext(New(), newResponseWriter(httptest.NewRecorder()), httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{}`)))
		if err := contextValue.BindJSON(destination); err == nil {
			t.Fatalf("BindJSON(%T) succeeded", destination)
		}
	}
	contextValue = newContext(New(), newResponseWriter(httptest.NewRecorder()), httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{} trailing`)))
	var destination map[string]any
	if err := contextValue.BindJSON(&destination); err == nil {
		t.Fatal("invalid trailing JSON succeeded")
	}
}

func TestResponseWriterCapabilityCombinations(t *testing.T) {
	base := httptest.NewRecorder()
	flush := &flushWriter{base}
	hijack := &hijackWriter{base}
	read := &readerFromWriter{base}
	push := &pushWriter{base}
	writers := []http.ResponseWriter{
		struct {
			http.ResponseWriter
			http.Flusher
			http.Hijacker
		}{base, flush, hijack},
		struct {
			http.ResponseWriter
			http.Flusher
			io.ReaderFrom
		}{base, flush, read},
		struct {
			http.ResponseWriter
			http.Hijacker
			io.ReaderFrom
		}{base, hijack, read},
		struct {
			http.ResponseWriter
			http.Hijacker
			http.Pusher
		}{base, hijack, push},
		struct {
			http.ResponseWriter
			http.Flusher
			http.Hijacker
			http.Pusher
		}{base, flush, hijack, push},
		struct {
			http.ResponseWriter
			io.ReaderFrom
			http.Pusher
		}{base, read, push},
		struct {
			http.ResponseWriter
			http.Flusher
			io.ReaderFrom
			http.Pusher
		}{base, flush, read, push},
		struct {
			http.ResponseWriter
			http.Hijacker
			io.ReaderFrom
			http.Pusher
		}{base, hijack, read, push},
	}
	for _, underlying := range writers {
		writer := newResponseWriter(underlying)
		if writer.capabilities == nil {
			t.Fatalf("capabilities missing for %T", underlying)
		}
	}
	writer := newResponseWriter(httptest.NewRecorder())
	if writer.Status() != http.StatusOK {
		t.Fatalf("initial status = %d", writer.Status())
	}
	writer.WriteHeader(http.StatusCreated)
	writer.WriteHeader(http.StatusTeapot)
	if writer.Status() != http.StatusCreated {
		t.Fatalf("second header changed status to %d", writer.Status())
	}
}

func TestBindingRemainingBranches(t *testing.T) {
	app := New()
	app.Post("/form", func(contextValue *Context) error {
		value, err := contextValue.FormValue("name")
		if err != nil {
			return err
		}
		return contextValue.Text(http.StatusOK, value)
	})
	request := httptest.NewRequest(http.MethodPost, "/form", strings.NewReader("name=Ada"))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()
	app.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Body.String() != "Ada" {
		t.Fatalf("form response = %d %q", response.Code, response.Body.String())
	}
	request = httptest.NewRequest(http.MethodPost, "/form", strings.NewReader("name=%zz"))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response = httptest.NewRecorder()
	app.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("malformed form status = %d", response.Code)
	}

	type tagged struct {
		Missing  string  `query:"missing"`
		Numbers  []int   `query:"number"`
		Pointer  *int    `query:"pointer"`
		Unsigned uint    `query:"unsigned"`
		Decimal  float64 `query:"decimal"`
		hidden   string  `query:"hidden"`
	}
	for _, rawQuery := range []string{
		"number=bad",
		"pointer=7",
		"unsigned=bad",
		"decimal=bad",
	} {
		contextValue := newContext(New(), newResponseWriter(httptest.NewRecorder()), httptest.NewRequest(http.MethodGet, "/?"+rawQuery, nil))
		var value tagged
		err := contextValue.BindQuery(&value)
		if rawQuery == "pointer=7" {
			if err != nil || value.Pointer == nil || *value.Pointer != 7 {
				t.Fatalf("pointer binding = %#v, %v", value.Pointer, err)
			}
		} else if err == nil {
			t.Fatalf("invalid query %q succeeded", rawQuery)
		}
	}
	contextValue := newContext(New(), newResponseWriter(httptest.NewRecorder()), httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{}`)))
	if err := contextValue.Bind(nil); err == nil {
		t.Fatal("nil Bind destination succeeded")
	}
	if err := contextValue.BindQuery(nil); err == nil {
		t.Fatal("nil tagged destination succeeded")
	}

	request = httptest.NewRequest(http.MethodPost, "/", strings.NewReader("broken"))
	request.Header.Set("Content-Type", "multipart/form-data; boundary=test")
	contextValue = newContext(New(), newResponseWriter(httptest.NewRecorder()), request)
	if _, _, err := contextValue.FormFile("file"); err == nil {
		t.Fatal("malformed multipart file succeeded")
	}
	request = httptest.NewRequest(http.MethodPost, "/", nil)
	request.Header.Set("Content-Type", `multipart/form-data; boundary="`)
	contextValue = newContext(New(), newResponseWriter(httptest.NewRecorder()), request)
	if err := contextValue.parseMultipartForm(); err == nil {
		t.Fatal("malformed multipart content type succeeded")
	}
}

func TestErrorRouteGroupAndHealthEdges(t *testing.T) {
	if got := (&HTTPError{Message: "message"}).Error(); got != "message" {
		t.Fatalf("HTTP error = %q", got)
	}
	for _, err := range []error{
		&OperationError{},
		ErrAsyncUnavailable,
		ErrInvalidOperation,
		ErrRetriesUnsupported,
	} {
		mapped := mapHTTPError(err)
		if mapped.code == "" || mapped.message == "" {
			t.Fatalf("mapped error = %#v", mapped)
		}
	}

	requireAsyncPanic(t, func() { New().Handle(http.MethodGet, "/", nil) })
	requireAsyncPanic(t, func() { New().HandleHTTP("/", nil) })
	group := New().Group("/")
	requireAsyncPanic(t, func() { group.Handle(http.MethodGet, "/", nil) })
	requireAsyncPanic(t, func() { group.HandleHTTP("/", nil) })
	if normalizeGroupPrefix("") != "" || normalizePath("") != "/" || joinPath("", "child") != "/child" || joinPath("/api", "/") != "/api/" {
		t.Fatal("path normalization edge failed")
	}
	if err := runHealthCheck(context.Background(), nil); err == nil {
		t.Fatal("nil health check succeeded")
	}
}

type coverageLifecycleComponent struct {
	startErr error
	done     chan error
}

func (component *coverageLifecycleComponent) Start(context.Context) error { return component.startErr }
func (component *coverageLifecycleComponent) Done() <-chan error          { return component.done }
func (*coverageLifecycleComponent) Shutdown(context.Context) error        { return nil }

func TestLifecycleAndModuleRaceEdges(t *testing.T) {
	finished := &coverageLifecycleComponent{done: make(chan error, 1)}
	finished.done <- nil
	if err := New().runLifecycle(nil, nil, finished); err != nil {
		t.Fatalf("nil-parent lifecycle = %v", err)
	}
	failed := &coverageLifecycleComponent{startErr: errors.New("start failed"), done: make(chan error)}
	if err := New().runLifecycle(context.Background(), failed); err == nil {
		t.Fatal("component start failure was ignored")
	}
	invalid := New()
	invalid.Get("/same", func(*Context) error { return nil })
	invalid.Get("/same", func(*Context) error { return nil })
	if err := invalid.runLifecycle(context.Background()); err == nil {
		t.Fatal("invalid application lifecycle started")
	}
	alreadyRunning := New()
	alreadyRunning.state = applicationRunning
	if err := alreadyRunning.runLifecycle(context.Background()); err == nil {
		t.Fatal("running application started twice")
	}
	contextValue, cancel := context.WithCancel(context.Background())
	cancel()
	if err := New().runLifecycle(contextValue); err != nil {
		t.Fatalf("empty cancelled lifecycle = %v", err)
	}

	registered := make(chan struct{})
	release := make(chan struct{})
	app := New()
	module := coverageModule{register: func(*Registrar) error {
		close(registered)
		<-release
		return nil
	}}
	result := make(chan error, 1)
	go func() { result <- app.Register(module) }()
	<-registered
	if err := app.Build(); err != nil {
		t.Fatalf("build application: %v", err)
	}
	close(release)
	if err := <-result; err == nil {
		t.Fatal("module committed after build")
	}
}

type coverageModule struct{ register func(*Registrar) error }

func (coverageModule) Name() string { return "coverage" }
func (module coverageModule) Register(registrar *Registrar) error {
	return module.register(registrar)
}

func TestClientIPParsingEdges(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set("X-Real-IP", "bad ip")
	if _, err := forwardedChain(request); err == nil {
		t.Fatal("invalid X-Real-IP succeeded")
	}
	request = httptest.NewRequest(http.MethodGet, "/", nil)
	if chain, err := forwardedChain(request); err != nil || chain != nil {
		t.Fatalf("empty forwarded chain = %#v, %v", chain, err)
	}
	for _, header := range []string{`for="unterminated`, `for=not-an-ip`} {
		request = httptest.NewRequest(http.MethodGet, "/", nil)
		request.Header.Set("Forwarded", header)
		if _, err := forwardedChain(request); err == nil {
			t.Fatalf("invalid Forwarded header %q succeeded", header)
		}
	}
	if address, err := parseRequestAddress("192.0.2.1:8080"); err != nil || address.String() != "192.0.2.1" {
		t.Fatalf("host-port address = %v, %v", address, err)
	}
}
