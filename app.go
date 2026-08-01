package vial

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
)

type requestContextKey struct{}

// App is the framework application and implements http.Handler.
type App struct {
	mu sync.RWMutex

	config       config
	routes       []routeDefinition
	middleware   []Middleware
	errorHandler ErrorHandler
	built        bool
	buildErr     error
	compiledRoot Handler
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

func (app *App) Get(path string, handler Handler) {
	app.Handle(http.MethodGet, path, handler)
}

func (app *App) Post(path string, handler Handler) {
	app.Handle(http.MethodPost, path, handler)
}

func (app *App) Put(path string, handler Handler) {
	app.Handle(http.MethodPut, path, handler)
}

func (app *App) Patch(path string, handler Handler) {
	app.Handle(http.MethodPatch, path, handler)
}

func (app *App) Delete(path string, handler Handler) {
	app.Handle(http.MethodDelete, path, handler)
}

func (app *App) Options(path string, handler Handler) {
	app.Handle(http.MethodOptions, path, handler)
}

func (app *App) Handle(method, path string, handler Handler) {
	if handler == nil {
		panic("vial: handler cannot be nil")
	}
	method = strings.ToUpper(strings.TrimSpace(method))
	path = normalizePath(path)
	app.addRoute(routeDefinition{
		route: Route{
			Method:  method,
			Path:    path,
			Pattern: routePattern(method, path),
		},
		handler: handler,
	})
}

// HandleHTTP mounts a standard-library handler using a native ServeMux pattern.
// Examples include "/metrics", "GET /health", and "api.example.com/".
func (app *App) HandleHTTP(pattern string, handler http.Handler) {
	if handler == nil {
		panic("vial: HTTP handler cannot be nil")
	}
	app.addRoute(routeDefinition{
		route:       Route{Path: pattern, Pattern: pattern},
		httpHandler: handler,
	})
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
	if app.built {
		panic("vial: application is already built; routes and middleware are immutable")
	}
}

// Build validates registrations and freezes the application routing graph.
func (app *App) Build() error {
	app.mu.Lock()
	defer app.mu.Unlock()

	if app.built {
		return app.buildErr
	}
	app.built = true

	mux := http.NewServeMux()
	for index := range app.routes {
		definition := app.routes[index]
		route := definition.route

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
	if err := root(contextValue); err != nil {
		errorHandler(contextValue, err)
	}
}
