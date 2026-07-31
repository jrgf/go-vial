package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestExampleNoteLifecycle(t *testing.T) {
	app := newApp(&noteStore{nextID: 1})

	createRequest := httptest.NewRequest(http.MethodPost, "/api/notes", strings.NewReader(`{"title":"First","body":"Body"}`))
	createRequest.Header.Set("Content-Type", "application/json")
	createdResponse := httptest.NewRecorder()
	app.ServeHTTP(createdResponse, createRequest)
	var created note
	if err := json.Unmarshal(createdResponse.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created note: %v", err)
	}
	if createdResponse.Code != http.StatusCreated || created.ID != 1 || created.Title != "First" {
		t.Fatalf("unexpected create response: status=%d note=%+v", createdResponse.Code, created)
	}

	listResponse := httptest.NewRecorder()
	app.ServeHTTP(listResponse, httptest.NewRequest(http.MethodGet, "/api/notes", nil))
	var notes []note
	if err := json.Unmarshal(listResponse.Body.Bytes(), &notes); err != nil {
		t.Fatalf("decode notes: %v", err)
	}
	if len(notes) != 1 || notes[0] != created {
		t.Fatalf("unexpected notes: %+v", notes)
	}

	invalidRequest := httptest.NewRequest(http.MethodPost, "/api/notes", strings.NewReader(`{"title":" "}`))
	invalidRequest.Header.Set("Content-Type", "application/json")
	invalidResponse := httptest.NewRecorder()
	app.ServeHTTP(invalidResponse, invalidRequest)
	if invalidResponse.Code != http.StatusBadRequest {
		t.Fatalf("invalid note status = %d", invalidResponse.Code)
	}
}
