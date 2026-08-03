package vial

import (
	"net/http"
	"strings"
)

// Group applies a common path prefix and middleware chain to related routes.
type Group struct {
	app        *App
	prefix     string
	middleware []Middleware
}

func normalizeGroupPrefix(prefix string) string {
	if prefix == "" || prefix == "/" {
		return ""
	}
	return strings.TrimSuffix(normalizePath(prefix), "/")
}

// Use adds middleware to routes subsequently registered through the group.
func (group *Group) Use(middleware ...Middleware) {
	group.app.mu.Lock()
	defer group.app.mu.Unlock()
	group.app.ensureMutableLocked()

	for _, item := range middleware {
		if item != nil {
			group.middleware = append(group.middleware, item)
		}
	}
}

// Group creates a nested group that inherits the current middleware chain.
func (group *Group) Group(prefix string) *Group {
	combined := normalizeGroupPrefix(joinPath(group.prefix, prefix))
	middleware := append([]Middleware(nil), group.middleware...)
	return &Group{app: group.app, prefix: combined, middleware: middleware}
}

// Get registers a GET route in the group.
func (group *Group) Get(path string, handler Handler, options ...RouteOption) {
	group.Handle(http.MethodGet, path, handler, options...)
}

// Post registers a POST route in the group.
func (group *Group) Post(path string, handler Handler, options ...RouteOption) {
	group.Handle(http.MethodPost, path, handler, options...)
}

// Put registers a PUT route in the group.
func (group *Group) Put(path string, handler Handler, options ...RouteOption) {
	group.Handle(http.MethodPut, path, handler, options...)
}

// Patch registers a PATCH route in the group.
func (group *Group) Patch(path string, handler Handler, options ...RouteOption) {
	group.Handle(http.MethodPatch, path, handler, options...)
}

// Delete registers a DELETE route in the group.
func (group *Group) Delete(path string, handler Handler, options ...RouteOption) {
	group.Handle(http.MethodDelete, path, handler, options...)
}

// Options registers an OPTIONS route in the group.
func (group *Group) Options(path string, handler Handler, options ...RouteOption) {
	group.Handle(http.MethodOptions, path, handler, options...)
}

// Handle registers a route in the group.
func (group *Group) Handle(method, path string, handler Handler, options ...RouteOption) {
	if handler == nil {
		panic("vial: handler cannot be nil")
	}

	method = strings.ToUpper(strings.TrimSpace(method))
	fullPath := joinPath(group.prefix, path)
	definition := newRouteDefinition(Route{
		Method:  method,
		Path:    fullPath,
		Pattern: routePattern(method, fullPath),
	}, options)
	definition.handler = handler
	definition.middleware = append(append([]Middleware(nil), group.middleware...), definition.middleware...)
	group.app.addRoute(definition)
}

// HandleHTTP registers a standard library handler under the group's prefix.
func (group *Group) HandleHTTP(pattern string, handler http.Handler, options ...RouteOption) {
	if handler == nil {
		panic("vial: HTTP handler cannot be nil")
	}

	pattern = strings.TrimSpace(pattern)
	if slash := strings.IndexByte(pattern, '/'); slash >= 0 {
		pattern = pattern[:slash] + joinPath(group.prefix, pattern[slash:])
	}
	definition := newRouteDefinition(routeFromHTTPPattern(pattern), options)
	definition.httpHandler = handler
	definition.middleware = append(append([]Middleware(nil), group.middleware...), definition.middleware...)
	group.app.addRoute(definition)
}
