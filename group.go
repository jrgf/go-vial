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

func (group *Group) Group(prefix string) *Group {
	combined := normalizeGroupPrefix(joinPath(group.prefix, prefix))
	middleware := append([]Middleware(nil), group.middleware...)
	return &Group{app: group.app, prefix: combined, middleware: middleware}
}

func (group *Group) Get(path string, handler Handler, options ...RouteOption) {
	group.Handle(http.MethodGet, path, handler, options...)
}

func (group *Group) Post(path string, handler Handler, options ...RouteOption) {
	group.Handle(http.MethodPost, path, handler, options...)
}

func (group *Group) Put(path string, handler Handler, options ...RouteOption) {
	group.Handle(http.MethodPut, path, handler, options...)
}

func (group *Group) Patch(path string, handler Handler, options ...RouteOption) {
	group.Handle(http.MethodPatch, path, handler, options...)
}

func (group *Group) Delete(path string, handler Handler, options ...RouteOption) {
	group.Handle(http.MethodDelete, path, handler, options...)
}

func (group *Group) Options(path string, handler Handler, options ...RouteOption) {
	group.Handle(http.MethodOptions, path, handler, options...)
}

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
	definition.middleware = append([]Middleware(nil), group.middleware...)
	group.app.addRoute(definition)
}
