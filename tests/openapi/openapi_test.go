package openapi_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
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

func TestSchemasMatchJSONFieldEncoding(t *testing.T) {
	type Base struct {
		ID string `json:"id" openapi:"required"`
	}
	type Other struct {
		ID string `json:"id"`
	}
	type private struct {
		Visible bool `json:"visible"`
	}
	type Plain struct{ ID int }
	type Tagged struct {
		Name string `json:"ID" openapi:"required"`
	}
	type Left struct{ Base }
	type Right struct{ Base }
	type Recursive struct {
		*Recursive
		Value string `json:"value"`
	}
	count := 7
	// Build unusual options at runtime so lint still catches accidental source tags.
	malformed := reflect.New(reflect.StructOf([]reflect.StructField{
		{Name: "Dash", Type: reflect.TypeFor[int](), Tag: reflect.StructTag(`json:"-,omitempty"`)},
		{Name: "Items", Type: reflect.TypeFor[[]string](), Tag: reflect.StructTag(`json:"items,string"`)},
	})).Elem()
	malformed.Field(0).SetInt(1)
	cases := []struct {
		name     string
		value    any
		types    map[string]string
		required []string
	}{
		{"embedded and quoted", struct {
			Base
			Count int  `json:"count,string"`
			Flag  bool `json:"flag,string"`
			Plain int  `json:"string"`
		}{Base{"x"}, 7, true, 3}, map[string]string{"id": "string", "count": "string", "flag": "string", "string": "integer"}, []string{"id"}},
		// Construct deliberate tag conflicts at runtime so go vet can still
		// reject accidental duplicate tags in application types.
		{"ambiguous", reflect.New(reflect.StructOf([]reflect.StructField{
			{Name: "Base", Type: reflect.TypeFor[Base](), Anonymous: true},
			{Name: "Other", Type: reflect.TypeFor[Other](), Anonymous: true},
			{Name: "Keep", Type: reflect.TypeFor[int]()},
		})).Elem().Interface(), map[string]string{"Keep": "integer"}, nil},
		{"shallower wins", struct {
			Base
			ID int `json:"id"`
		}{}, map[string]string{"id": "integer"}, nil},
		{"tag wins", struct {
			Plain
			Tagged
		}{}, map[string]string{"ID": "string"}, []string{"ID"}},
		{"unexported embedding", struct{ private }{}, map[string]string{"visible": "boolean"}, nil},
		{"named embedding", struct {
			Base `json:"base"`
		}{}, map[string]string{"base": "object"}, nil},
		{"diamond", reflect.New(reflect.StructOf([]reflect.StructField{
			{Name: "Left", Type: reflect.TypeFor[Left](), Anonymous: true},
			{Name: "Right", Type: reflect.TypeFor[Right](), Anonymous: true},
			{Name: "Keep", Type: reflect.TypeFor[bool]()},
		})).Elem().Interface(), map[string]string{"Keep": "boolean"}, nil},
		{"recursive embedding", Recursive{}, map[string]string{"value": "string"}, nil},
		{"literal dash and ignored options", malformed.Interface(), map[string]string{"-": "integer", "items": "array"}, nil},
		{"quoted pointer", struct {
			Count *int `json:"count,string"`
		}{&count}, map[string]string{"count": "nullable-string"}, nil},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			app := vial.New()
			app.Post("/", func(*vial.Context) error { return nil }, vial.RouteName("example"))
			encoded, err := openapi.Generate(app, openapi.Config{Title: "API", Version: "1", Operations: map[string]openapi.Operation{
				"example": {Request: test.value, Responses: map[int]openapi.Response{200: {Body: test.value}}},
			}})
			if err != nil {
				t.Fatal(err)
			}
			var generated document
			if err := json.Unmarshal(encoded, &generated); err != nil {
				t.Fatal(err)
			}
			wire, err := json.Marshal(test.value)
			if err != nil {
				t.Fatal(err)
			}
			var actual map[string]any
			if err := json.Unmarshal(wire, &actual); err != nil {
				t.Fatal(err)
			}
			operation := generated.Paths["/"]["post"]
			for _, schema := range []map[string]any{operation.RequestBody.Content["application/json"].Schema, operation.Responses["200"].Content["application/json"].Schema} {
				properties := schema["properties"].(map[string]any)
				if len(properties) != len(actual) {
					t.Fatalf("properties=%v actual JSON=%s", properties, wire)
				}
				for name := range actual {
					property, ok := properties[name].(map[string]any)
					if !ok {
						t.Fatalf("JSON field %q missing from schema", name)
					}
					want := test.types[name]
					if want == "nullable-string" {
						choices := property["anyOf"].([]any)
						if choices[0].(map[string]any)["type"] != "string" || choices[1].(map[string]any)["type"] != "null" {
							t.Fatalf("quoted pointer schema=%v", property)
						}
					} else if property["type"] != want {
						t.Errorf("field %q type=%v want=%s", name, property["type"], want)
					}
				}
				var required []string
				names, _ := schema["required"].([]any)
				for _, name := range names {
					required = append(required, name.(string))
				}
				if !slices.Equal(required, test.required) {
					t.Fatalf("required=%v want=%v", required, test.required)
				}
			}
		})
	}
}

type customSchemaValue struct{ Unsupported chan int }

func TestNonportableJSONTagsRequireAnExplicitSchema(t *testing.T) {
	model := reflect.New(reflect.StructOf([]reflect.StructField{
		{Name: "Invalid", Type: reflect.TypeFor[int](), Tag: reflect.StructTag(`json:"bad\\name"`)},
	})).Elem().Interface()
	app := vial.New()
	app.Post("/", func(*vial.Context) error { return nil }, vial.RouteName("example"))
	for _, operation := range []openapi.Operation{
		{Request: model}, {Responses: map[int]openapi.Response{200: {Body: model}}},
	} {
		config := openapi.Config{Title: "API", Version: "1", Operations: map[string]openapi.Operation{"example": operation}}
		if _, err := openapi.Generate(app, config); err == nil || !strings.Contains(err.Error(), "nonportable JSON tag") {
			t.Fatalf("nonportable tag should need an override: %v", err)
		}
		operation.RequestSchema = json.RawMessage(`{"type":"object"}`)
		operation.Responses = map[int]openapi.Response{200: {Body: model, Schema: json.RawMessage(`{"type":"object"}`)}}
		config.Operations["example"] = operation
		if _, err := openapi.Generate(app, config); err != nil {
			t.Fatalf("explicit schema should bypass nonportable inference: %v", err)
		}
	}
}

func (customSchemaValue) MarshalJSON() ([]byte, error) {
	panic("schema generation must not call MarshalJSON")
}

type customSchemaInteger struct{ Value int64 }

func (value customSchemaInteger) MarshalJSON() ([]byte, error) { return json.Marshal(value.Value) }

func TestExplicitSchemasAndSnapshot(t *testing.T) {
	app := vial.New()
	response := customSchemaInteger{Value: 7}
	app.Post("/items/{id}", func(c *vial.Context) error { return c.JSON(http.StatusOK, response) }, vial.RouteName("items"))
	requestSchema := json.RawMessage(`{"type":"object","properties":{"name":{"type":"string","minLength":1,"enum":["one","two"]}},"required":["name"],"examples":[{"name":"one"}]}`)
	responseSchema := json.RawMessage(`{"type":"integer","maximum":9007199254740993,"examples":[7]}`)
	config := openapi.Config{Title: "API", Version: "1", Operations: map[string]openapi.Operation{
		"items": {
			Request: struct {
				ID          int `path:"id"`
				Unsupported chan int
			}{},
			RequestSchema: requestSchema, RequestRequired: true,
			Responses: map[int]openapi.Response{200: {Body: response, Schema: responseSchema}},
		},
	}}
	handler, err := openapi.Handler(app, config)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := openapi.Generate(app, config)
	if err != nil {
		t.Fatal(err)
	}
	requestSchema[0], responseSchema[0] = '[', '['
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/openapi.json", nil))
	if recorder.Code != http.StatusOK || !bytes.Equal(encoded, recorder.Body.Bytes()) {
		t.Fatalf("mutating caller schemas changed the document: %s", recorder.Body.String())
	}
	var generated document
	if err := json.Unmarshal(encoded, &generated); err != nil {
		t.Fatal(err)
	}
	operation := generated.Paths["/items/{id}"]["post"]
	if len(operation.Parameters) != 1 || operation.Parameters[0].Schema["type"] != "integer" || !operation.RequestBody.Required {
		t.Fatalf("schema override lost parameter or required metadata: %+v", operation)
	}
	var want map[string]any
	if err := json.Unmarshal(json.RawMessage(`{"type":"object","properties":{"name":{"type":"string","minLength":1,"enum":["one","two"]}},"required":["name"],"examples":[{"name":"one"}]}`), &want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(operation.RequestBody.Content["application/json"].Schema, want) || !bytes.Contains(encoded, []byte("9007199254740993")) {
		t.Fatalf("schema constraints or numeric precision changed: %s", encoded)
	}
	recorder = httptest.NewRecorder()
	app.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/items/1", nil))
	if recorder.Code != http.StatusOK || strings.TrimSpace(recorder.Body.String()) != "7" || operation.Responses["200"].Content["application/json"].Schema["type"] != "integer" {
		t.Fatalf("custom response disagrees with its schema: %s", recorder.Body.String())
	}
}

func TestSchemaValidationAndBooleanOverrides(t *testing.T) {
	app := vial.New()
	app.Post("/", func(*vial.Context) error { return nil }, vial.RouteName("example"))
	for _, raw := range []string{"", "[", "null", "[]", "42", `"schema"`, "{} {}", "\u00a0{}"} {
		for _, request := range []bool{true, false} {
			operation := openapi.Operation{}
			if request {
				operation.RequestSchema = json.RawMessage(raw)
			} else {
				operation.Responses = map[int]openapi.Response{200: {Schema: json.RawMessage(raw)}}
			}
			if _, err := openapi.Handler(app, openapi.Config{Title: "API", Version: "1", Operations: map[string]openapi.Operation{"example": operation}}); err == nil {
				t.Errorf("accepted invalid schema %q", raw)
			}
		}
	}
	for _, raw := range []string{"true", "false", "{}"} {
		encoded, err := openapi.Generate(app, openapi.Config{Title: "API", Version: "1", Operations: map[string]openapi.Operation{
			"example": {RequestSchema: json.RawMessage(raw), RequestContentType: "application/json", Responses: map[int]openapi.Response{200: {Schema: json.RawMessage(raw)}}},
		}})
		if err != nil || !bytes.Contains(encoded, []byte(`"schema": `+raw)) {
			t.Fatalf("schema %s: document=%s error=%v", raw, encoded, err)
		}
	}
	encoded, err := openapi.Generate(app, openapi.Config{Title: "API", Version: "1", Operations: map[string]openapi.Operation{
		"example": {Request: customSchemaValue{}, Responses: map[int]openapi.Response{200: {Body: customSchemaValue{}}}},
	}})
	if err != nil || !bytes.Contains(encoded, []byte(`"schema": {}`)) {
		t.Fatalf("custom marshalers need an unconstrained fallback: %s %v", encoded, err)
	}
}
