package main

import (
	"net/http"
	"os"
	"testing"

	"github.com/jrgf/go-vial/testkit"
)

func TestDatabaseExample(t *testing.T) {
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		t.Skip("DATABASE_URL is not set")
	}
	app, err := newApp(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	server := testkit.Start(t, app)
	server.Do(server.NewRequest(http.MethodGet, "/ready", nil)).RequireStatus(http.StatusOK)
	response := server.JSON(http.MethodPost, "/notes", createNoteRequest{Body: "transactional note"})
	response.RequireStatus(http.StatusCreated)
	var created note
	response.Decode(&created)
	if created.ID == 0 || created.Body != "transactional note" || created.CreatedAt.IsZero() {
		t.Fatalf("created note = %#v", created)
	}
}
