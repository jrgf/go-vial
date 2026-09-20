package openapi

import (
	"encoding/json"
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
	Summary     string
	Description string
	Tags        []string
	Request     any
	// RequestSchema replaces inferred body metadata with a JSON Schema object
	// or boolean. It does not change runtime binding or validation.
	RequestSchema      json.RawMessage
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
	// Schema replaces the inferred body schema, including for custom marshalers.
	Schema      json.RawMessage
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
