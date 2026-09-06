package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"time"

	vial "github.com/jrgf/go-vial"
	"github.com/jrgf/go-vial/openapi"
)

type createNoteRequest struct {
	Title string `json:"title" openapi:"required"`
	Body  string `json:"body"`
}

type note struct {
	ID        int64     `json:"id" openapi:"required"`
	Title     string    `json:"title" openapi:"required"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at" openapi:"required"`
}

func main() {
	app, err := newApp()
	if err != nil {
		log.Fatal(err)
	}
	log.Print("API listening on http://localhost:8080; document at /openapi.json")
	if err := app.Run(context.Background(), ":8080"); err != nil {
		log.Fatal(err)
	}
}

func newApp() (*vial.App, error) {
	app := vial.New(vial.WithDisallowUnknownJSONFields(true))
	app.SetErrorHandler(vial.ProblemDetailsErrorHandler)
	app.Post("/notes", func(contextValue *vial.Context) error {
		var request createNoteRequest
		if err := contextValue.BindJSON(&request); err != nil {
			return err
		}
		request.Title = strings.TrimSpace(request.Title)
		if request.Title == "" {
			return vial.BadRequest("title_required", "title is required")
		}
		location, err := contextValue.App().URL("notes.get", map[string]string{"id": "1"})
		if err != nil {
			return err
		}
		contextValue.Response().Header().Set("Location", location)
		return contextValue.JSON(http.StatusCreated, note{
			ID:        1,
			Title:     request.Title,
			Body:      request.Body,
			CreatedAt: time.Now().UTC(),
		})
	}, vial.RouteName("notes.create"))
	app.Get("/notes/{id}", func(contextValue *vial.Context) error {
		return contextValue.JSON(http.StatusOK, note{
			ID:        1,
			Title:     "OpenAPI",
			CreatedAt: time.Now().UTC(),
		})
	}, vial.RouteName("notes.get"))

	err := openapi.Mount(app, "/openapi.json", openapi.Config{
		Title:   "Notes API",
		Version: "1.0.0",
		Operations: map[string]openapi.Operation{
			"notes.create": {
				Summary:         "Create a note",
				Request:         createNoteRequest{},
				RequestRequired: true,
				// Documentation only; the handler above enforces the title rule.
				RequestSchema: json.RawMessage(`{
					"type": "object",
					"properties": {
						"title": {"type": "string", "minLength": 1, "pattern": "\\S"},
						"body": {"type": "string"}
					},
					"required": ["title"],
					"additionalProperties": false,
					"examples": [{"title": "Release checklist", "body": "Run the checks"}]
				}`),
				Responses: map[int]openapi.Response{
					http.StatusCreated: {Body: note{}},
					http.StatusBadRequest: {
						Description: "Invalid note", ContentType: "application/problem+json",
						Schema: json.RawMessage(`{
							"type": "object",
							"properties": {
								"type": {"const": "about:blank"},
								"title": {"const": "Bad Request"},
								"status": {"const": 400},
								"detail": {"type": "string"},
								"code": {"type": "string"},
								"fields": {"type": "object", "additionalProperties": {"type": "string"}}
							},
							"required": ["type", "title", "status", "detail", "code"]
						}`),
					},
				},
			},
			"notes.get": {
				Summary: "Get a note",
				Request: struct {
					ID int64 `path:"id" openapi:"required"`
				}{},
				Responses: map[int]openapi.Response{http.StatusOK: {Body: note{}}},
			},
		},
	})
	if err != nil {
		return nil, err
	}
	return app, nil
}
