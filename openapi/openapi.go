package openapi

import (
	"context"
	"encoding"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"mime"
	"net/http"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	vial "github.com/jrgf/go-vial"
)

const openAPIVersion = "3.1.0"

var (
	textMarshalerType = reflect.TypeOf((*encoding.TextMarshaler)(nil)).Elem()
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

// Config describes one OpenAPI document. Operations are keyed by RouteName.
type Config struct {
	Title           string
	Version         string
	Description     string
	Operations      map[string]Operation
	SecuritySchemes map[string]SecurityScheme
	Security        []SecurityRequirement
}

// Operation adds documentation to a named Vial route. Nil Security inherits
// Config.Security; an empty non-nil slice marks the operation as public.
type Operation struct {
	Summary            string
	Description        string
	Tags               []string
	Request            any
	RequestContentType string
	RequestRequired    bool
	Responses          map[int]Response
	Security           []SecurityRequirement
	Hidden             bool
}

// Response documents one HTTP response.
type Response struct {
	Description string
	Body        any
	ContentType string
}

// SecurityScheme documents an HTTP or API-key authentication scheme.
type SecurityScheme struct {
	Type         string
	Description  string
	Scheme       string
	BearerFormat string
	Name         string
	In           string
}

// SecurityRequirement maps scheme names to required OAuth scopes. HTTP and
// API-key schemes use an empty scope slice.
type SecurityRequirement map[string][]string

type documentHandler struct {
	app           *vial.App
	config        Config
	once          sync.Once
	document      []byte
	generationErr error
}

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

	paths := make(map[string]any)
	seen := make(map[string]bool, len(config.Operations))
	for _, route := range routes {
		operation, configured := config.Operations[route.Name]
		if configured {
			seen[route.Name] = true
			if operation.Hidden {
				continue
			}
		}
		if route.Method == "" {
			// ponytail: OpenAPI has no methodless operation. Register raw handlers
			// with an explicit method when they must appear in the document.
			if configured {
				return nil, fmt.Errorf("openapi: configured route %q has no HTTP method", route.Name)
			}
			continue
		}
		method := strings.ToLower(route.Method)
		if !supportedMethods[method] {
			return nil, fmt.Errorf("openapi: route %q uses unsupported method %q", route.Pattern, route.Method)
		}
		path := documentPath(route.Path)
		pathItem, _ := paths[path].(map[string]any)
		if pathItem == nil {
			pathItem = make(map[string]any)
			paths[path] = pathItem
		}
		if _, exists := pathItem[method]; exists {
			return nil, fmt.Errorf("openapi: duplicate operation %s %s", route.Method, path)
		}
		generated, err := generateOperation(route, operation)
		if err != nil {
			return nil, err
		}
		pathItem[method] = generated
	}
	for name := range config.Operations {
		if !seen[name] {
			return nil, fmt.Errorf("openapi: operation %q does not match a named route", name)
		}
	}

	info := map[string]any{"title": config.Title, "version": config.Version}
	if config.Description != "" {
		info["description"] = config.Description
	}
	document := map[string]any{
		"openapi":           openAPIVersion,
		"jsonSchemaDialect": "https://json-schema.org/draft/2020-12/schema",
		"info":              info,
		"paths":             paths,
	}
	if len(config.SecuritySchemes) > 0 {
		schemes := make(map[string]any, len(config.SecuritySchemes))
		for name, scheme := range config.SecuritySchemes {
			schemes[name] = securitySchemeDocument(scheme)
		}
		document["components"] = map[string]any{"securitySchemes": schemes}
	}
	if config.Security != nil {
		document["security"] = config.Security
	}

	encoded, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("openapi: encode document: %w", err)
	}
	return append(encoded, '\n'), nil
}

// Handler returns a handler that generates the document once, after application
// registration has finished.
func Handler(app *vial.App, config Config) (http.Handler, error) {
	return newDocumentHandler(app, config)
}

func newDocumentHandler(app *vial.App, config Config) (*documentHandler, error) {
	if app == nil {
		return nil, errors.New("openapi: nil application")
	}
	if err := validateConfig(config); err != nil {
		return nil, err
	}
	config = cloneConfig(config)
	return &documentHandler{app: app, config: config}, nil
}

func (handler *documentHandler) load() error {
	handler.once.Do(func() { handler.document, handler.generationErr = Generate(handler.app, handler.config) })
	return handler.generationErr
}

func (handler *documentHandler) ServeHTTP(response http.ResponseWriter, _ *http.Request) {
	if err := handler.load(); err != nil {
		http.Error(response, "OpenAPI document unavailable", http.StatusInternalServerError)
		return
	}
	response.Header().Set("Content-Type", "application/vnd.oai.openapi+json;version=3.1")
	_, _ = response.Write(handler.document)
}

// Mount registers a GET endpoint that serves the generated document.
func Mount(app *vial.App, path string, config Config) error {
	if !strings.HasPrefix(path, "/") || strings.ContainsAny(path, " \t\r\n?#") {
		return fmt.Errorf("openapi: invalid documentation path %q", path)
	}
	handler, err := newDocumentHandler(app, config)
	if err != nil {
		return err
	}
	app.HandleHTTP(http.MethodGet+" "+path, handler)
	app.OnStart(func(context.Context) error { return handler.load() })
	return nil
}

func generateOperation(route vial.Route, configured Operation) (map[string]any, error) {
	operation := make(map[string]any)
	if route.Name != "" {
		operation["operationId"] = route.Name
	}
	if configured.Summary != "" {
		operation["summary"] = configured.Summary
	}
	if configured.Description != "" {
		operation["description"] = configured.Description
	}
	tags := configured.Tags
	if len(tags) == 0 && route.Module != "" {
		tags = []string{route.Module}
	}
	if len(tags) > 0 {
		operation["tags"] = tags
	}

	parameters, requestBody, err := describeRequest(route, configured)
	if err != nil {
		return nil, fmt.Errorf("openapi: route %q: %w", route.Pattern, err)
	}
	if len(parameters) > 0 {
		operation["parameters"] = parameters
	}
	if requestBody != nil {
		operation["requestBody"] = requestBody
	}
	responses, err := describeResponses(configured.Responses)
	if err != nil {
		return nil, fmt.Errorf("openapi: route %q: %w", route.Pattern, err)
	}
	operation["responses"] = responses
	if configured.Security != nil {
		operation["security"] = configured.Security
	}
	return operation, nil
}

func describeRequest(route vial.Route, operation Operation) ([]any, map[string]any, error) {
	parameters := make([]any, 0, len(route.Parameters))
	indexes := make(map[string]int)
	pathParameters := make(map[string]bool, len(route.Parameters))
	for _, name := range route.Parameters {
		pathParameters[name] = true
		indexes["path\x00"+name] = len(parameters)
		parameters = append(parameters, map[string]any{
			"name": name, "in": "path", "required": true,
			"schema": map[string]any{"type": "string"},
		})
	}
	if operation.Request == nil {
		return parameters, nil, nil
	}

	requestType := reflect.TypeOf(operation.Request)
	for requestType.Kind() == reflect.Pointer {
		requestType = requestType.Elem()
	}
	if requestType.Kind() == reflect.Struct {
		for index := 0; index < requestType.NumField(); index++ {
			field := requestType.Field(index)
			if !field.IsExported() {
				continue
			}
			for _, source := range []string{"path", "query", "header", "cookie"} {
				name := tagName(field.Tag.Get(source))
				if name == "" || name == "-" {
					continue
				}
				if source == "path" && !pathParameters[name] {
					return nil, nil, fmt.Errorf("request path field %q is not present in the route", name)
				}
				schema, err := schemaFor(field.Type, make(map[reflect.Type]bool))
				if err != nil {
					return nil, nil, fmt.Errorf("request field %s: %w", field.Name, err)
				}
				key := parameterKey(source, name)
				parameter := map[string]any{"name": name, "in": source, "schema": schema}
				if source == "path" || requiredField(field) {
					parameter["required"] = true
				}
				if existing, ok := indexes[key]; ok {
					parameters[existing] = parameter
					continue
				}
				indexes[key] = len(parameters)
				parameters = append(parameters, parameter)
			}
		}
	}

	contentType := operation.RequestContentType
	if contentType == "" {
		contentType = "application/json"
	}
	bodySchema, present, err := requestSchema(requestType, contentType)
	if err != nil {
		return nil, nil, err
	}
	if !present {
		return parameters, nil, nil
	}
	requestBody := map[string]any{
		"content": map[string]any{contentType: map[string]any{"schema": bodySchema}},
	}
	if operation.RequestRequired {
		requestBody["required"] = true
	}
	return parameters, requestBody, nil
}

func requestSchema(requestType reflect.Type, contentType string) (map[string]any, bool, error) {
	if requestType.Kind() != reflect.Struct {
		schema, err := schemaFor(requestType, make(map[reflect.Type]bool))
		return schema, true, err
	}
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return nil, false, fmt.Errorf("invalid request content type %q", contentType)
	}
	form := mediaType == "application/x-www-form-urlencoded" || mediaType == "multipart/form-data"
	properties := make(map[string]any)
	required := make([]string, 0)
	for index := 0; index < requestType.NumField(); index++ {
		field := requestType.Field(index)
		if !field.IsExported() {
			continue
		}
		name, include := requestFieldName(field, form)
		if !include {
			continue
		}
		schema, err := schemaFor(field.Type, make(map[reflect.Type]bool))
		if err != nil {
			return nil, false, fmt.Errorf("request field %s: %w", field.Name, err)
		}
		properties[name] = schema
		if requiredField(field) {
			required = append(required, name)
		}
	}
	if len(properties) == 0 {
		return nil, false, nil
	}
	schema := map[string]any{"type": "object", "properties": properties}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema, true, nil
}

func requestFieldName(field reflect.StructField, form bool) (string, bool) {
	if form {
		name := tagName(field.Tag.Get("form"))
		return name, name != "" && name != "-"
	}
	jsonTag, hasJSONTag := field.Tag.Lookup("json")
	name := tagName(jsonTag)
	if name == "-" {
		return "", false
	}
	explicit := false
	for _, source := range []string{"path", "query", "header", "cookie", "form"} {
		candidate := tagName(field.Tag.Get(source))
		if candidate != "" && candidate != "-" {
			explicit = true
			break
		}
	}
	if explicit && !hasJSONTag {
		return "", false
	}
	if name == "" {
		name = field.Name
	}
	return name, true
}

func describeResponses(configured map[int]Response) (map[string]any, error) {
	if len(configured) == 0 {
		return map[string]any{"200": map[string]any{"description": http.StatusText(http.StatusOK)}}, nil
	}
	responses := make(map[string]any, len(configured))
	for status, configuredResponse := range configured {
		if status < 100 || status > 599 {
			return nil, fmt.Errorf("invalid response status %d", status)
		}
		description := configuredResponse.Description
		if description == "" {
			description = http.StatusText(status)
			if description == "" {
				description = "Response"
			}
		}
		response := map[string]any{"description": description}
		if configuredResponse.Body != nil || configuredResponse.ContentType != "" {
			schema := map[string]any{}
			if configuredResponse.Body != nil {
				var err error
				schema, err = schemaFor(reflect.TypeOf(configuredResponse.Body), make(map[reflect.Type]bool))
				if err != nil {
					return nil, fmt.Errorf("response %d: %w", status, err)
				}
			}
			contentType := configuredResponse.ContentType
			if contentType == "" {
				contentType = "application/json"
			}
			if _, _, err := mime.ParseMediaType(contentType); err != nil {
				return nil, fmt.Errorf("invalid response content type %q", contentType)
			}
			response["content"] = map[string]any{contentType: map[string]any{"schema": schema}}
		}
		responses[strconv.Itoa(status)] = response
	}
	return responses, nil
}

func schemaFor(valueType reflect.Type, visiting map[reflect.Type]bool) (map[string]any, error) {
	if valueType == nil {
		return map[string]any{}, nil
	}
	if valueType.Kind() == reflect.Pointer {
		value, err := schemaFor(valueType.Elem(), visiting)
		if err != nil {
			return nil, err
		}
		return map[string]any{"anyOf": []any{value, map[string]any{"type": "null"}}}, nil
	}
	if valueType == timeType {
		return map[string]any{"type": "string", "format": "date-time"}, nil
	}
	if valueType == rawMessageType {
		return map[string]any{}, nil
	}
	if valueType.Implements(textMarshalerType) || reflect.PointerTo(valueType).Implements(textMarshalerType) {
		return map[string]any{"type": "string"}, nil
	}

	switch valueType.Kind() {
	case reflect.Bool:
		return map[string]any{"type": "boolean"}, nil
	case reflect.Int:
		return map[string]any{"type": "integer"}, nil
	case reflect.Int8, reflect.Int16, reflect.Int32:
		return map[string]any{"type": "integer", "format": "int32"}, nil
	case reflect.Int64:
		return map[string]any{"type": "integer", "format": "int64"}, nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return map[string]any{"type": "integer", "minimum": 0}, nil
	case reflect.Float32:
		return map[string]any{"type": "number", "format": "float"}, nil
	case reflect.Float64:
		return map[string]any{"type": "number", "format": "double"}, nil
	case reflect.String:
		return map[string]any{"type": "string"}, nil
	case reflect.Slice:
		if valueType.Elem().Kind() == reflect.Uint8 {
			return map[string]any{"type": "string", "contentEncoding": "base64"}, nil
		}
		items, err := schemaFor(valueType.Elem(), visiting)
		return map[string]any{"type": "array", "items": items}, err
	case reflect.Array:
		items, err := schemaFor(valueType.Elem(), visiting)
		return map[string]any{"type": "array", "items": items, "minItems": valueType.Len(), "maxItems": valueType.Len()}, err
	case reflect.Map:
		value, err := schemaFor(valueType.Elem(), visiting)
		return map[string]any{"type": "object", "additionalProperties": value}, err
	case reflect.Interface:
		return map[string]any{}, nil
	case reflect.Struct:
		if visiting[valueType] {
			// ponytail: inline schemas collapse recursive edges. Add component
			// references if exact recursive schemas become necessary.
			return map[string]any{"type": "object"}, nil
		}
		visiting[valueType] = true
		defer delete(visiting, valueType)
		properties := make(map[string]any)
		required := make([]string, 0)
		for index := 0; index < valueType.NumField(); index++ {
			field := valueType.Field(index)
			if !field.IsExported() {
				continue
			}
			name := tagName(field.Tag.Get("json"))
			if name == "-" {
				continue
			}
			if name == "" {
				name = field.Name
			}
			property, err := schemaFor(field.Type, visiting)
			if err != nil {
				return nil, fmt.Errorf("field %s: %w", field.Name, err)
			}
			properties[name] = property
			if requiredField(field) {
				required = append(required, name)
			}
		}
		schema := map[string]any{"type": "object", "properties": properties}
		if len(required) > 0 {
			schema["required"] = required
		}
		return schema, nil
	default:
		return nil, fmt.Errorf("unsupported Go type %s", valueType)
	}
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

func tagName(tag string) string {
	name, _, _ := strings.Cut(tag, ",")
	return name
}

func requiredField(field reflect.StructField) bool {
	for option := range strings.SplitSeq(field.Tag.Get("openapi"), ",") {
		if strings.TrimSpace(option) == "required" {
			return true
		}
	}
	return false
}

func parameterKey(source, name string) string {
	if source == "header" {
		name = strings.ToLower(name)
	}
	return source + "\x00" + name
}

func validateConfig(config Config) error {
	if strings.TrimSpace(config.Title) == "" {
		return errors.New("openapi: title is required")
	}
	if strings.TrimSpace(config.Version) == "" {
		return errors.New("openapi: version is required")
	}
	for name, operation := range config.Operations {
		if strings.TrimSpace(name) == "" {
			return errors.New("openapi: operation route name is required")
		}
		if operation.Request == nil && operation.RequestContentType != "" {
			return fmt.Errorf("openapi: operation %q sets a content type without a request", name)
		}
		if operation.RequestContentType != "" {
			if _, _, err := mime.ParseMediaType(operation.RequestContentType); err != nil {
				return fmt.Errorf("openapi: operation %q has invalid request content type %q", name, operation.RequestContentType)
			}
		}
		for status, response := range operation.Responses {
			if status < 100 || status > 599 {
				return fmt.Errorf("openapi: operation %q has invalid response status %d", name, status)
			}
			if response.ContentType != "" {
				if _, _, err := mime.ParseMediaType(response.ContentType); err != nil {
					return fmt.Errorf("openapi: operation %q has invalid response content type %q", name, response.ContentType)
				}
			}
		}
	}
	for name, scheme := range config.SecuritySchemes {
		if strings.TrimSpace(name) == "" {
			return errors.New("openapi: security scheme name is required")
		}
		switch scheme.Type {
		case "http":
			if strings.TrimSpace(scheme.Scheme) == "" {
				return fmt.Errorf("openapi: HTTP security scheme %q requires a scheme", name)
			}
		case "apiKey":
			if strings.TrimSpace(scheme.Name) == "" {
				return fmt.Errorf("openapi: API-key security scheme %q requires a name", name)
			}
			if scheme.In != "header" && scheme.In != "query" && scheme.In != "cookie" {
				return fmt.Errorf("openapi: API-key security scheme %q has invalid location %q", name, scheme.In)
			}
		default:
			return fmt.Errorf("openapi: security scheme %q has unsupported type %q", name, scheme.Type)
		}
	}
	if err := validateSecurityRequirements(config.Security, config.SecuritySchemes); err != nil {
		return err
	}
	for name, operation := range config.Operations {
		if err := validateSecurityRequirements(operation.Security, config.SecuritySchemes); err != nil {
			return fmt.Errorf("openapi: operation %q: %w", name, err)
		}
	}
	return nil
}

func validateSecurityRequirements(requirements []SecurityRequirement, schemes map[string]SecurityScheme) error {
	for _, requirement := range requirements {
		for name, scopes := range requirement {
			if _, ok := schemes[name]; !ok {
				return fmt.Errorf("security requirement references unknown scheme %q", name)
			}
			if len(scopes) != 0 {
				return fmt.Errorf("security scheme %q does not accept OAuth scopes", name)
			}
		}
	}
	return nil
}

func securitySchemeDocument(scheme SecurityScheme) map[string]any {
	document := map[string]any{"type": scheme.Type}
	if scheme.Description != "" {
		document["description"] = scheme.Description
	}
	if scheme.Type == "http" {
		document["scheme"] = scheme.Scheme
		if scheme.BearerFormat != "" {
			document["bearerFormat"] = scheme.BearerFormat
		}
	} else {
		document["name"] = scheme.Name
		document["in"] = scheme.In
	}
	return document
}

func cloneConfig(config Config) Config {
	clone := config
	clone.Security = cloneRequirements(config.Security)
	clone.SecuritySchemes = maps.Clone(config.SecuritySchemes)
	clone.Operations = make(map[string]Operation, len(config.Operations))
	for name, operation := range config.Operations {
		operation.Tags = slices.Clone(operation.Tags)
		operation.Security = cloneRequirements(operation.Security)
		operation.Responses = maps.Clone(operation.Responses)
		clone.Operations[name] = operation
	}
	return clone
}

func cloneRequirements(requirements []SecurityRequirement) []SecurityRequirement {
	if requirements == nil {
		return nil
	}
	clone := make([]SecurityRequirement, len(requirements))
	for index, requirement := range requirements {
		clone[index] = make(SecurityRequirement, len(requirement))
		for name, scopes := range requirement {
			clone[index][name] = slices.Clone(scopes)
		}
	}
	return clone
}
