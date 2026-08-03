package vial

import (
	"net/http"
	"strings"
)

// Route contains metadata for a registered endpoint.
type Route struct {
	Method          string
	Path            string
	Pattern         string
	Name            string
	Module          string
	MiddlewareCount int
	Parameters      []string
}

// RouteOption configures route metadata.
type RouteOption struct {
	name       string
	hasName    bool
	middleware []Middleware
}

// RouteName assigns a globally unique, stable observability and diagnostics
// identifier. Route names are not used for URL generation.
func RouteName(name string) RouteOption {
	return RouteOption{name: name, hasName: true}
}

// RouteMiddleware applies middleware only to the configured route.
func RouteMiddleware(middleware ...Middleware) RouteOption {
	filtered := make([]Middleware, 0, len(middleware))
	for _, item := range middleware {
		if item != nil {
			filtered = append(filtered, item)
		}
	}
	return RouteOption{middleware: filtered}
}

type routeDefinition struct {
	route       Route
	handler     Handler
	httpHandler http.Handler
	middleware  []Middleware
	hasName     bool
}

func newRouteDefinition(route Route, options []RouteOption) routeDefinition {
	route.Parameters = routeParameterNames(route.Pattern)
	definition := routeDefinition{route: route}
	for _, option := range options {
		if option.hasName {
			definition.route.Name = option.name
			definition.hasName = true
		}
		definition.middleware = append(definition.middleware, option.middleware...)
	}
	return definition
}

func routeFromHTTPPattern(pattern string) Route {
	pattern = strings.TrimSpace(pattern)
	route := Route{Pattern: pattern}
	target := pattern
	if space := strings.IndexByte(pattern, ' '); space >= 0 {
		route.Method = pattern[:space]
		target = strings.TrimSpace(pattern[space+1:])
	}
	if slash := strings.IndexByte(target, '/'); slash >= 0 {
		route.Path = target[slash:]
	} else {
		route.Path = target
	}
	return route
}

func routeParameterNames(pattern string) []string {
	parameters := make([]string, 0)
	for {
		start := strings.IndexByte(pattern, '{')
		if start < 0 {
			return parameters
		}
		pattern = pattern[start+1:]
		end := strings.IndexByte(pattern, '}')
		if end < 0 {
			return parameters
		}
		name := strings.TrimSuffix(pattern[:end], "...")
		if name != "$" && name != "" {
			parameters = append(parameters, name)
		}
		pattern = pattern[end+1:]
	}
}

func validRegistrationName(name string) bool {
	return name != "" && name == strings.TrimSpace(name) && !strings.ContainsAny(name, "\r\n\t")
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
