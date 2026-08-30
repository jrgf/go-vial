package openapi_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	vial "github.com/jrgf/go-vial"
	"github.com/jrgf/go-vial/openapi"
	"github.com/jrgf/go-vial/testkit"
)

type createWidgetRequest struct {
	ID      int64   `path:"id"`
	Query   string  `query:"q" openapi:"required"`
	TraceID string  `header:"X-Trace-ID"`
	Name    string  `json:"name" openapi:"required"`
	Note    *string `json:"note,omitempty"`
	Ignored string  `json:"-"`
}

type widget struct {
	ID        int64     `json:"id" openapi:"required"`
	Name      string    `json:"name" openapi:"required"`
	CreatedAt time.Time `json:"created_at"`
	Next      *widget   `json:"next,omitempty"`
}

type document struct {
	OpenAPI string `json:"openapi"`
	Info    struct {
		Title   string `json:"title"`
		Version string `json:"version"`
	} `json:"info"`
	Paths map[string]map[string]struct {
		OperationID string `json:"operationId"`
		Parameters  []struct {
			Name     string         `json:"name"`
			In       string         `json:"in"`
			Required bool           `json:"required"`
			Schema   map[string]any `json:"schema"`
		} `json:"parameters"`
		RequestBody struct {
			Required bool `json:"required"`
			Content  map[string]struct {
				Schema map[string]any `json:"schema"`
			} `json:"content"`
		} `json:"requestBody"`
		Responses map[string]struct {
			Description string `json:"description"`
			Content     map[string]struct {
				Schema map[string]any `json:"schema"`
			} `json:"content"`
		} `json:"responses"`
	} `json:"paths"`
	Components struct {
		SecuritySchemes map[string]map[string]any `json:"securitySchemes"`
	} `json:"components"`
	Security []openapi.SecurityRequirement `json:"security"`
}

func TestGenerateAndServe(t *testing.T) {
	app := vial.New()
	app.Post("/widgets/{id}", func(*vial.Context) error { return nil }, vial.RouteName("widgets.create"))
	app.Get("/events/{topic...}", func(*vial.Context) error { return nil }, vial.RouteName("events.watch"))
	app.Get("/slash/", func(*vial.Context) error { return nil })
	app.HandleHTTP("/assets/", http.NotFoundHandler())

	config := openapi.Config{
		Title:   "Widget API",
		Version: "1.0.0",
		SecuritySchemes: map[string]openapi.SecurityScheme{
			"bearer": {Type: "http", Scheme: "bearer", BearerFormat: "JWT"},
		},
		Security: []openapi.SecurityRequirement{{"bearer": {}}},
		Operations: map[string]openapi.Operation{
			"widgets.create": {
				Summary:         "Create a widget",
				Request:         createWidgetRequest{},
				RequestRequired: true,
				Responses: map[int]openapi.Response{
					http.StatusCreated:    {Body: widget{}},
					http.StatusBadRequest: {Description: "Invalid widget"},
				},
			},
			"events.watch": {Responses: map[int]openapi.Response{http.StatusOK: {ContentType: "text/event-stream"}}},
		},
	}
	if err := openapi.Mount(app, "/openapi.json", config); err != nil {
		t.Fatalf("mount: %v", err)
	}

	generated, err := openapi.Generate(app, config)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	again, err := openapi.Generate(app, config)
	if err != nil {
		t.Fatalf("generate again: %v", err)
	}
	if !bytes.Equal(generated, again) {
		t.Fatal("generated document is not deterministic")
	}

	var decoded document
	if err := json.Unmarshal(generated, &decoded); err != nil {
		t.Fatalf("decode document: %v", err)
	}
	if decoded.OpenAPI != "3.1.0" || decoded.Info.Title != "Widget API" || decoded.Info.Version != "1.0.0" {
		t.Fatalf("document header = %#v", decoded)
	}
	operation, ok := decoded.Paths["/widgets/{id}"]["post"]
	if !ok || operation.OperationID != "widgets.create" {
		t.Fatalf("widget operation = %#v", operation)
	}
	if len(operation.Parameters) != 3 || operation.Parameters[0].Name != "id" || operation.Parameters[0].Schema["format"] != "int64" || !operation.Parameters[1].Required {
		t.Fatalf("parameters = %#v", operation.Parameters)
	}
	body := operation.RequestBody.Content["application/json"].Schema
	properties := body["properties"].(map[string]any)
	if !operation.RequestBody.Required || len(properties) != 2 || properties["name"] == nil || properties["note"] == nil {
		t.Fatalf("request body = %#v", operation.RequestBody)
	}
	created := operation.Responses["201"]
	responseProperties := created.Content["application/json"].Schema["properties"].(map[string]any)
	if created.Description != "Created" || responseProperties["created_at"].(map[string]any)["format"] != "date-time" {
		t.Fatalf("created response = %#v", created)
	}
	if _, ok := decoded.Paths["/events/{topic}"]; !ok {
		t.Fatalf("wildcard path was not normalized: %#v", decoded.Paths)
	}
	if _, ok := decoded.Paths["/slash/"]; !ok {
		t.Fatalf("trailing slash path was not preserved: %#v", decoded.Paths)
	}
	if _, ok := decoded.Paths["/assets/"]; ok {
		t.Fatal("methodless handler appeared in document")
	}
	if decoded.Components.SecuritySchemes["bearer"]["scheme"] != "bearer" || len(decoded.Security) != 1 {
		t.Fatalf("security = %#v %#v", decoded.Components, decoded.Security)
	}

	server := testkit.Start(t, app)
	response := server.Do(server.NewRequest(http.MethodGet, "/openapi.json", nil))
	response.RequireStatus(http.StatusOK)
	if got := response.Header.Get("Content-Type"); got != "application/vnd.oai.openapi+json;version=3.1" {
		t.Fatalf("content type = %q", got)
	}
	if response.Text() != string(generated) {
		t.Fatal("served document differs from generated document")
	}

	bad := config
	bad.Operations = map[string]openapi.Operation{"missing": {}}
	if _, err := openapi.Generate(app, bad); err == nil {
		t.Fatal("unknown named operation returned nil")
	}
	if _, err := openapi.Handler(app, openapi.Config{}); err == nil {
		t.Fatal("invalid configuration returned nil")
	}
}
