package vial

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
)

type requestContextKey struct{}

type applicationState uint8

const (
	applicationCreated applicationState = iota
	applicationRegistering
	applicationBuilt
	applicationStarting
	applicationRunning
	applicationStopping
	applicationStopped
)

type routeMissWriter struct {
	header http.Header
	status int
}

func (writer *routeMissWriter) Header() http.Header {
	return writer.header
}

func (writer *routeMissWriter) WriteHeader(status int) {
	if writer.status == 0 {
		writer.status = status
	}
}

func (writer *routeMissWriter) Write(body []byte) (int, error) {
	writer.WriteHeader(http.StatusOK)
	return len(body), nil
}

// App is the framework application and implements http.Handler.
type App struct {
	mu sync.RWMutex

	config       config
	routes       []routeDefinition
	modules      []string
	middleware   []Middleware
	errorHandler ErrorHandler
	state        applicationState
	buildErr     error
	compiledRoot Handler
	startHooks   []LifecycleHook
	stopHooks    []LifecycleHook
}

func New(options ...Option) *App {
	cfg := defaultConfig()
	for _, option := range options {
		if option != nil {
			option(&cfg)
		}
	}

	return &App{
		config:       cfg,
		errorHandler: defaultErrorHandler,
	}
}

func (app *App) Use(middleware ...Middleware) {
	app.mu.Lock()
	defer app.mu.Unlock()
	app.ensureMutableLocked()

	for _, item := range middleware {
		if item != nil {
			app.middleware = append(app.middleware, item)
		}
	}
}

func (app *App) SetErrorHandler(handler ErrorHandler) {
	app.mu.Lock()
	defer app.mu.Unlock()
	app.ensureMutableLocked()

	if handler != nil {
		app.errorHandler = handler
	}
}

func (app *App) Get(path string, handler Handler, options ...RouteOption) {
	app.Handle(http.MethodGet, path, handler, options...)
}

func (app *App) Post(path string, handler Handler, options ...RouteOption) {
	app.Handle(http.MethodPost, path, handler, options...)
}

func (app *App) Put(path string, handler Handler, options ...RouteOption) {
	app.Handle(http.MethodPut, path, handler, options...)
}

func (app *App) Patch(path string, handler Handler, options ...RouteOption) {
	app.Handle(http.MethodPatch, path, handler, options...)
}

func (app *App) Delete(path string, handler Handler, options ...RouteOption) {
	app.Handle(http.MethodDelete, path, handler, options...)
}

func (app *App) Options(path string, handler Handler, options ...RouteOption) {
	app.Handle(http.MethodOptions, path, handler, options...)
}

func (app *App) Handle(method, path string, handler Handler, options ...RouteOption) {
	if handler == nil {
		panic("vial: handler cannot be nil")
	}
	method = strings.ToUpper(strings.TrimSpace(method))
	path = normalizePath(path)
	definition := newRouteDefinition(Route{
		Method:  method,
		Path:    path,
		Pattern: routePattern(method, path),
	}, options)
	definition.handler = handler
	app.addRoute(definition)
}

// HandleHTTP mounts a standard-library handler using a native ServeMux pattern.
// Examples include "/metrics", "GET /health", and "api.example.com/".
func (app *App) HandleHTTP(pattern string, handler http.Handler, options ...RouteOption) {
	if handler == nil {
		panic("vial: HTTP handler cannot be nil")
	}
	definition := newRouteDefinition(Route{Path: pattern, Pattern: pattern}, options)
	definition.httpHandler = handler
	app.addRoute(definition)
}

func (app *App) Group(prefix string) *Group {
	return &Group{app: app, prefix: normalizeGroupPrefix(prefix)}
}

func (app *App) addRoute(definition routeDefinition) {
	app.mu.Lock()
	defer app.mu.Unlock()
	app.ensureMutableLocked()
	app.routes = append(app.routes, definition)
}

func (app *App) ensureMutableLocked() {
	if app.state >= applicationBuilt {
		panic("vial: application is already built; registration is immutable")
	}
	app.state = applicationRegistering
}

// Build validates and freezes application registrations.
func (app *App) Build() error {
	app.mu.Lock()
	defer app.mu.Unlock()

	if app.state >= applicationBuilt {
		return app.buildErr
	}
	app.state = applicationBuilt

	mux := http.NewServeMux()
	moduleNames := make(map[string]struct{}, len(app.modules))
	for _, name := range app.modules {
		if !validRegistrationName(name) {
			app.buildErr = fmt.Errorf("invalid module name %q", name)
			return app.buildErr
		}
		if _, exists := moduleNames[name]; exists {
			app.buildErr = fmt.Errorf("duplicate module name %q", name)
			return app.buildErr
		}
		moduleNames[name] = struct{}{}
	}

	names := make(map[string]Route)
	for index := range app.routes {
		definition := app.routes[index]
		route := definition.route
		if definition.hasName {
			if !validRegistrationName(route.Name) {
				app.buildErr = fmt.Errorf("route %q has invalid name %q", route.Pattern, route.Name)
				return app.buildErr
			}
			if existing, exists := names[route.Name]; exists {
				app.buildErr = fmt.Errorf("duplicate route name %q for %q and %q", route.Name, existing.Pattern, route.Pattern)
				return app.buildErr
			}
			names[route.Name] = route
		}

		var endpoint Handler
		if definition.handler != nil {
			endpoint = chain(definition.handler, definition.middleware...)
		} else {
			raw := definition.httpHandler
			endpoint = func(context *Context) error {
				raw.ServeHTTP(context.response, context.request)
				return nil
			}
		}

		httpHandler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			contextValue, ok := request.Context().Value(requestContextKey{}).(*Context)
			if !ok || contextValue == nil {
				// This should only happen if a route handler is invoked outside App.ServeHTTP.
				response := newResponseWriter(writer)
				contextValue = newContext(app, response, request)
			}
			contextValue.route = &route
			contextValue.routeErr = endpoint(contextValue)
		})

		if err := safeMuxHandle(mux, route.Pattern, httpHandler); err != nil {
			app.buildErr = fmt.Errorf("register route %q: %w", route.Pattern, err)
			return app.buildErr
		}
	}

	dispatch := func(contextValue *Context) error {
		requestContext := context.WithValue(
			contextValue.request.Context(),
			requestContextKey{},
			contextValue,
		)
		contextValue.request = contextValue.request.WithContext(requestContext)
		if err := routeMiss(mux, contextValue.request); err != nil {
			return err
		}
		mux.ServeHTTP(contextValue.response, contextValue.request)
		return contextValue.routeErr
	}

	app.compiledRoot = chain(dispatch, app.middleware...)
	return nil
}

// Routes validates the application and returns registered route metadata in
// registration order. The returned slice does not share state with the app.
func (app *App) Routes() ([]Route, error) {
	if err := app.Build(); err != nil {
		return nil, err
	}

	app.mu.RLock()
	defer app.mu.RUnlock()

	routes := make([]Route, len(app.routes))
	for index := range app.routes {
		routes[index] = app.routes[index].route
	}
	return routes, nil
}

func safeMuxHandle(mux *http.ServeMux, pattern string, handler http.Handler) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("%v", recovered)
		}
	}()
	mux.Handle(pattern, handler)
	return nil
}

func routeMiss(mux *http.ServeMux, request *http.Request) *HTTPError {
	handler, pattern := mux.Handler(request)
	if pattern != "" {
		return nil
	}

	probe := &routeMissWriter{header: make(http.Header)}
	handler.ServeHTTP(probe, request)
	if probe.status == http.StatusMethodNotAllowed {
		err := MethodNotAllowed("method_not_allowed", http.StatusText(http.StatusMethodNotAllowed))
		err.Headers = make(http.Header)
		err.Headers.Set("Allow", probe.header.Get("Allow"))
		return err
	}
	return NotFound("not_found", http.StatusText(http.StatusNotFound))
}

func (app *App) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if err := app.Build(); err != nil {
		app.config.logger.Error("framework build failed", "error", err)
		http.Error(writer, "framework initialization failed", http.StatusInternalServerError)
		return
	}

	root := app.compiledRoot
	errorHandler := app.errorHandler

	response := newResponseWriter(writer)
	contextValue := newContext(app, response, request)
	defer contextValue.cleanup()
	if err := root(contextValue); err != nil {
		applyHTTPErrorHeaders(contextValue, err)
		errorHandler(contextValue, err)
	}
}
