package openapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"

	vial "github.com/jrgf/go-vial"
)

type documentHandler struct {
	app           *vial.App
	config        Config
	once          sync.Once
	document      []byte
	generationErr error
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

func generateOperation(route vial.Route, configured Operation) (*operationDocument, error) {
	operation := &operationDocument{OperationID: route.Name, Summary: configured.Summary, Description: configured.Description, Tags: configured.Tags}
	if len(operation.Tags) == 0 && route.Module != "" {
		operation.Tags = []string{route.Module}
	}
	parameters, requestBody, err := describeRequest(route, configured)
	if err != nil {
		return nil, fmt.Errorf("openapi: route %q: %w", route.Pattern, err)
	}
	operation.Parameters, operation.RequestBody = parameters, requestBody
	responses, err := describeResponses(configured.Responses)
	if err != nil {
		return nil, fmt.Errorf("openapi: route %q: %w", route.Pattern, err)
	}
	operation.Responses = responses
	if configured.Security != nil {
		operation.Security = &configured.Security
	}
	return operation, nil
}
