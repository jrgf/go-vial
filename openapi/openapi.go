package openapi

import (
	"encoding"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	vial "github.com/jrgf/go-vial"
)

const openAPIVersion = "3.1.0"

var (
	textMarshalerType = reflect.TypeOf((*encoding.TextMarshaler)(nil)).Elem()
	jsonMarshalerType = reflect.TypeOf((*json.Marshaler)(nil)).Elem()
	timeType          = reflect.TypeOf(time.Time{})
	rawMessageType    = reflect.TypeOf(json.RawMessage{})
	supportedMethods  = map[string]bool{
		"delete":  true,
		"get":     true,
		"head":    true,
		"options": true,
		"patch":   true,
		"post":    true,
		"put":     true,
		"trace":   true,
	}
)

// Generate builds deterministic, indented OpenAPI 3.1 JSON. Calling it freezes
// application registration through App.Routes.
func Generate(app *vial.App, config Config) ([]byte, error) {
	if app == nil {
		return nil, errors.New("openapi: nil application")
	}
	if err := validateConfig(config); err != nil {
		return nil, err
	}
	config = cloneConfig(config)
	routes, err := app.Routes()
	if err != nil {
		return nil, fmt.Errorf("openapi: build application: %w", err)
	}
	paths, err := generatePaths(routes, config)
	if err != nil {
		return nil, err
	}
	document := document{OpenAPI: openAPIVersion, JSONSchemaDialect: "https://json-schema.org/draft/2020-12/schema", Info: documentInfo{Title: config.Title, Version: config.Version, Description: config.Description}, Paths: paths}
	document.Components = configComponents(config)
	if config.Security != nil {
		document.Security = &config.Security
	}
	encoded, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("openapi: encode document: %w", err)
	}
	return append(encoded, '\n'), nil
}

func generatePaths(routes []vial.Route, config Config) (map[string]map[string]*operationDocument, error) {
	paths := make(map[string]map[string]*operationDocument)
	primaryRoutes := make(map[string]vial.Route)
	seen := make(map[string]bool, len(config.Operations))
	for _, route := range routes {
		operation, configured := config.Operations[route.Name]
		if configured {
			seen[route.Name] = true
		}
		include, err := documentedRoute(route, operation, configured)
		if err != nil {
			return nil, err
		}
		if !include {
			continue
		}
		generated, err := generateOperation(route, operation)
		if err != nil {
			return nil, err
		}
		addPathOperation(paths, primaryRoutes, route, generated)
	}
	for name := range config.Operations {
		if !seen[name] {
			return nil, fmt.Errorf("openapi: operation %q does not match a named route", name)
		}
	}
	return paths, nil
}

func documentedRoute(route vial.Route, operation Operation, configured bool) (bool, error) {
	if operation.Hidden {
		return false, nil
	}
	if route.Method == "" {
		// OpenAPI has no methodless operation; configured routes need a method.
		if configured {
			return false, fmt.Errorf("openapi: configured route %q has no HTTP method", route.Name)
		}
		return false, nil
	}
	if !supportedMethods[strings.ToLower(route.Method)] {
		return false, fmt.Errorf("openapi: route %q uses unsupported method %q", route.Pattern, route.Method)
	}
	return true, nil
}

func addPathOperation(paths map[string]map[string]*operationDocument, primaryRoutes map[string]vial.Route, route vial.Route, generated *operationDocument) {
	path, method := documentPath(route.Path), strings.ToLower(route.Method)
	if paths[path] == nil {
		paths[path] = make(map[string]*operationDocument)
	}
	key := method + " " + path
	if primary := paths[path][method]; primary != nil {
		previousRoute := primaryRoutes[key]
		if catchAllPath(previousRoute.Pattern) && !catchAllPath(route.Pattern) {
			generated.Alternatives = append([]*operationDocument{primary}, primary.Alternatives...)
			primary.Alternatives = nil
			primary.Pattern = previousRoute.Pattern
		} else {
			generated.Pattern = route.Pattern
			primary.Alternatives = append(primary.Alternatives, generated)
			return
		}
	}
	paths[path][method], primaryRoutes[key] = generated, route
}

func catchAllPath(path string) bool {
	return strings.HasSuffix(path, "/") || strings.HasSuffix(path, "...}")
}

func documentPath(path string) string {
	path = strings.ReplaceAll(path, "{$}", "")
	var documented strings.Builder
	for {
		start := strings.IndexByte(path, '{')
		if start < 0 {
			documented.WriteString(path)
			return documented.String()
		}
		endOffset := strings.IndexByte(path[start:], '}')
		if endOffset < 0 {
			documented.WriteString(path)
			return documented.String()
		}
		end := start + endOffset
		name := strings.TrimSuffix(path[start+1:end], "...")
		documented.WriteString(path[:start+1])
		documented.WriteString(name)
		documented.WriteByte('}')
		path = path[end+1:]
	}
}

func configComponents(config Config) *componentsDocument {
	if len(config.SecuritySchemes) == 0 {
		return nil
	}
	schemes := make(map[string]securityDocument, len(config.SecuritySchemes))
	for name, scheme := range config.SecuritySchemes {
		schemes[name] = securitySchemeDocument(scheme)
	}
	return &componentsDocument{SecuritySchemes: schemes}
}
