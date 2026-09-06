package vial

import (
	"fmt"
	"net/http"
	"net/url"
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

// RouteName assigns a globally unique identifier for observability,
// diagnostics, and URL generation through App.URL.
func RouteName(name string) RouteOption {
	return RouteOption{name: name, hasName: true}
}

// URL returns a root-relative, escaped path for a named route. Group prefixes
// are included; host patterns do not add an authority. Calling URL validates
// and freezes registration, like Routes. It is safe for concurrent use.
//
// Parameters are raw, unescaped values and must match the route's names exactly.
// A {name} parameter is one segment; {name...} splits on slash and may be empty
// or end with a slash. Empty interior segments, dot segments, and a slash-only
// single parameter are rejected because ServeMux cannot match them as intended.
// Add query parameters separately with net/url.Values.
func (app *App) URL(name string, parameters map[string]string) (string, error) {
	if !app.built.Load() {
		if err := app.Build(); err != nil {
			return "", err
		}
	}
	route, ok := app.namedRoutes[name]
	if !ok {
		return "", fmt.Errorf("vial: unknown route name %q", name)
	}
	if len(parameters) != len(route.Parameters) {
		return "", fmt.Errorf("vial: route %q requires %d parameters, got %d", name, len(route.Parameters), len(parameters))
	}
	path := strings.TrimSuffix(route.Path, "{$}")
	if strings.HasPrefix(path, "//") {
		return "", fmt.Errorf("vial: route %q does not have a root-relative URL path", name)
	}
	segments := strings.Split(path, "/")
	for index, segment := range segments {
		if !strings.HasPrefix(segment, "{") || !strings.HasSuffix(segment, "}") {
			if decoded, err := url.PathUnescape(segment); err == nil {
				segment = decoded
			}
			if segment != "." && segment != ".." {
				segments[index] = url.PathEscape(segment)
			}
			continue
		}
		parameter := segment[1 : len(segment)-1]
		wildcard := strings.HasSuffix(parameter, "...")
		parameter = strings.TrimSuffix(parameter, "...")
		value, present := parameters[parameter]
		if !present {
			return "", fmt.Errorf("vial: route %q is missing parameter %q", name, parameter)
		}
		parts := []string{value}
		if wildcard {
			parts = strings.Split(value, "/")
		}
		for partIndex, part := range parts {
			if part == "." || part == ".." || part == "/" || (part == "" && (!wildcard || partIndex != len(parts)-1)) {
				return "", fmt.Errorf("vial: route %q parameter %q contains an empty, slash-only, or dot segment", name, parameter)
			}
			parts[partIndex] = url.PathEscape(part)
		}
		segments[index] = strings.Join(parts, "/")
	}
	return strings.Join(segments, "/"), nil
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
