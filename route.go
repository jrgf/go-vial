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
}

type routeDefinition struct {
	route       Route
	handler     Handler
	httpHandler http.Handler
	middleware  []Middleware
}

func routePattern(method, path string) string {
	if method == "" {
		return path
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
