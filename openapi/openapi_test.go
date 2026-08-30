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

type textValue string

func (textValue) MarshalText() ([]byte, error) { return []byte("value"), nil }

type schemaShapes struct {
	Bool     bool            `json:"bool"`
	Int      int             `json:"int"`
	Int32    int32           `json:"int32"`
	Uint     uint            `json:"uint"`
	Uint64   uint64          `json:"uint64"`
	Float32  float32         `json:"float32"`
	Float64  float64         `json:"float64"`
	Bytes    []byte          `json:"bytes"`
	List     []string        `json:"list"`
	Fixed    [2]string       `json:"fixed"`
	Map      map[string]int  `json:"map"`
	Raw      json.RawMessage `json:"raw"`
	Text     textValue       `json:"text"`
	Anything any             `json:"anything"`
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
	app.Get("/shapes", func(*vial.Context) error { return nil }, vial.RouteName("shapes.get"))
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
			"shapes.get":   {Responses: map[int]openapi.Response{http.StatusOK: {Body: schemaShapes{}}}},
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
	shapes := decoded.Paths["/shapes"]["get"].Responses["200"].Content["application/json"].Schema["properties"].(map[string]any)
	if shapes["bool"].(map[string]any)["type"] != "boolean" || shapes["bytes"].(map[string]any)["contentEncoding"] != "base64" || shapes["text"].(map[string]any)["type"] != "string" {
		t.Fatalf("shape schemas = %#v", shapes)
	}

	config.Title = "mutated"
	delete(config.Operations, "widgets.create")
	delete(config.SecuritySchemes, "bearer")
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

func TestConfigurationValidation(t *testing.T) {
	validScheme := map[string]openapi.SecurityScheme{"auth": {Type: "http", Scheme: "bearer"}}
	cases := []openapi.Config{
		{},
		{Title: "API"},
		{Title: "API", Version: "1", Operations: map[string]openapi.Operation{"": {}}},
		{Title: "API", Version: "1", Operations: map[string]openapi.Operation{"route": {RequestContentType: "application/json"}}},
		{Title: "API", Version: "1", Operations: map[string]openapi.Operation{"route": {Request: struct{}{}, RequestContentType: "not a media type"}}},
		{Title: "API", Version: "1", Operations: map[string]openapi.Operation{"route": {Responses: map[int]openapi.Response{99: {}}}}},
		{Title: "API", Version: "1", Operations: map[string]openapi.Operation{"route": {Responses: map[int]openapi.Response{200: {ContentType: "not a media type"}}}}},
		{Title: "API", Version: "1", SecuritySchemes: map[string]openapi.SecurityScheme{"": {Type: "http", Scheme: "basic"}}},
		{Title: "API", Version: "1", SecuritySchemes: map[string]openapi.SecurityScheme{"auth": {Type: "http"}}},
		{Title: "API", Version: "1", SecuritySchemes: map[string]openapi.SecurityScheme{"auth": {Type: "apiKey", In: "header"}}},
		{Title: "API", Version: "1", SecuritySchemes: map[string]openapi.SecurityScheme{"auth": {Type: "apiKey", Name: "key", In: "body"}}},
		{Title: "API", Version: "1", SecuritySchemes: map[string]openapi.SecurityScheme{"auth": {Type: "oauth2"}}},
		{Title: "API", Version: "1", Security: []openapi.SecurityRequirement{{"missing": {}}}},
		{Title: "API", Version: "1", SecuritySchemes: validScheme, Security: []openapi.SecurityRequirement{{"auth": {"scope"}}}},
		{Title: "API", Version: "1", SecuritySchemes: validScheme, Operations: map[string]openapi.Operation{"route": {Security: []openapi.SecurityRequirement{{"missing": {}}}}}},
	}
	for index, config := range cases {
		if _, err := openapi.Handler(vial.New(), config); err == nil {
			t.Fatalf("case %d returned nil", index)
		}
	}
	if _, err := openapi.Handler(nil, openapi.Config{Title: "API", Version: "1"}); err == nil {
		t.Fatal("nil application returned nil")
	}
}
