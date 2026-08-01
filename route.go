package vial

import (
	"net/http"
	"strings"
)

// Route contains metadata for a registered endpoint.
type Route struct {
	Method  string
	Path    string
	Pattern string
	Name    string
}

// RouteOption configures route metadata.
type RouteOption struct {
	name    string
	hasName bool
}

// RouteName assigns a stable name to a route.
func RouteName(name string) RouteOption {
	return RouteOption{name: name, hasName: true}
}

type routeDefinition struct {
	route       Route
	handler     Handler
	httpHandler http.Handler
	middleware  []Middleware
	hasName     bool
}

func newRouteDefinition(route Route, options []RouteOption) routeDefinition {
	definition := routeDefinition{route: route}
	for _, option := range options {
		if option.hasName {
			definition.route.Name = option.name
			definition.hasName = true
		}
	}
	return definition
}

func routePattern(method, path string) string {
	if method == "" {
		return path
	}
	if strings.HasSuffix(path, "/") {
		path += "{$}"
	}
	return strings.ToUpper(method) + " " + path
}

func normalizePath(path string) string {
	if path == "" {
		return "/"
	}
	if !strings.HasPrefix(path, "/") {
		return "/" + path
	}
	return path
}

func joinPath(prefix, path string) string {
	if prefix == "" || prefix == "/" {
		return normalizePath(path)
	}

	prefix = strings.TrimSuffix(normalizePath(prefix), "/")
	if path == "" {
		return prefix
	}
	path = normalizePath(path)
	if path == "/" {
		return prefix + "/"
	}
	return prefix + path
}
