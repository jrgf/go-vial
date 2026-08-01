package main

import (
	"net/http"
	"testing"

	"github.com/jrgf/go-vial/testkit"
)

func TestExampleNoteLifecycle(t *testing.T) {
	server := testkit.Start(t, newApp(&noteStore{nextID: 1}))

	createdResponse := server.JSON(http.MethodPost, "/api/notes", map[string]string{
		"title": "First",
		"body":  "Body",
	})
	createdResponse.RequireStatus(http.StatusCreated)
	var created note
	createdResponse.Decode(&created)
	if created.ID != 1 || created.Title != "First" {
		t.Fatalf("unexpected created note: %+v", created)
	}

	listResponse := server.Do(server.NewRequest(http.MethodGet, "/api/notes", nil))
	listResponse.RequireStatus(http.StatusOK)
	var notes []note
	listResponse.Decode(&notes)
	if len(notes) != 1 || notes[0] != created {
		t.Fatalf("unexpected notes: %+v", notes)
	}

	invalidResponse := server.JSON(http.MethodPost, "/api/notes", map[string]string{"title": " "})
	invalidResponse.RequireStatus(http.StatusBadRequest)
}
