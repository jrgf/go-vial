package openapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"mime"
	"slices"
	"strings"
)

func validateConfig(config Config) error {
	if strings.TrimSpace(config.Title) == "" || strings.TrimSpace(config.Version) == "" {
		return errors.New("openapi: title and version are required")
	}
	for name, operation := range config.Operations {
		if strings.TrimSpace(name) == "" {
			return errors.New("openapi: operation route name is required")
		}
		if err := validateOperation(operation); err != nil {
			return fmt.Errorf("openapi: operation %q: %w", name, err)
		}
	}
	for name, scheme := range config.SecuritySchemes {
		if strings.TrimSpace(name) == "" {
			return errors.New("openapi: security scheme name is required")
		}
		if err := validateSecurityScheme(name, scheme); err != nil {
			return err
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

func validateOperation(operation Operation) error {
	if err := validateSchema(operation.RequestSchema); err != nil {
		return fmt.Errorf("request schema: %w", err)
	}
	if operation.Request == nil && len(operation.RequestSchema) == 0 && operation.RequestContentType != "" {
		return errors.New("content type without a request")
	}
	if operation.RequestContentType != "" {
		if _, _, err := mime.ParseMediaType(operation.RequestContentType); err != nil {
			return fmt.Errorf("invalid request content type %q", operation.RequestContentType)
		}
	}
	for status, response := range operation.Responses {
		if err := validateSchema(response.Schema); err != nil {
			return fmt.Errorf("response %d schema: %w", status, err)
		}
		if status < 100 || status > 599 {
			return fmt.Errorf("invalid response status %d", status)
		}
		if response.ContentType != "" {
			if _, _, err := mime.ParseMediaType(response.ContentType); err != nil {
				return fmt.Errorf("invalid response content type %q", response.ContentType)
			}
		}
	}
	return nil
}

func validateSecurityScheme(name string, scheme SecurityScheme) error {
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

func securitySchemeDocument(scheme SecurityScheme) securityDocument {
	result := securityDocument{Type: scheme.Type, Description: scheme.Description}
	if scheme.Type == "http" {
		result.Scheme, result.BearerFormat = scheme.Scheme, scheme.BearerFormat
	} else {
		result.Name, result.In = scheme.Name, scheme.In
	}
	return result
}

func cloneConfig(config Config) Config {
	clone := config
	clone.Security = cloneRequirements(config.Security)
	clone.SecuritySchemes = maps.Clone(config.SecuritySchemes)
	clone.Operations = make(map[string]Operation, len(config.Operations))
	for name, operation := range config.Operations {
		operation.RequestSchema = slices.Clone(operation.RequestSchema)
		operation.Tags = slices.Clone(operation.Tags)
		operation.Security = cloneRequirements(operation.Security)
		operation.Responses = maps.Clone(operation.Responses)
		for status, response := range operation.Responses {
			response.Schema = slices.Clone(response.Schema)
			operation.Responses[status] = response
		}
		clone.Operations[name] = operation
	}
	return clone
}

func validateSchema(schema json.RawMessage) error {
	if schema == nil {
		return nil
	}
	trimmed := bytes.TrimSpace(schema)
	if !json.Valid(schema) || (trimmed[0] != '{' && !bytes.Equal(trimmed, []byte("true")) && !bytes.Equal(trimmed, []byte("false"))) {
		return errors.New("expected a JSON Schema object or boolean")
	}
	return nil
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
