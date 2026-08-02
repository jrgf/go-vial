package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"

	"github.com/jrgf/go-vial"
	"github.com/jrgf/go-vial/middleware"
)

type createNoteRequest struct {
	Title string `json:"title"`
	Body  string `json:"body"`
}

type note struct {
	ID    int    `json:"id"`
	Title string `json:"title"`
	Body  string `json:"body"`
}

type noteStore struct {
	mu     sync.RWMutex
	nextID int
	notes  []note
}

func main() {
	store := &noteStore{nextID: 1}
	app := newApp(store)
	address := os.Getenv("ADDR")
	if address == "" {
		address = ":8080"
	}

	if err := app.Run(context.Background(), address); err != nil {
		slog.Error("application stopped with an error", "error", err)
		os.Exit(1)
	}
}

func newApp(store *noteStore) *vial.App {
	app := vial.New(vial.WithDisallowUnknownJSONFields(true))
	app.Use(middleware.RequestID(), middleware.Logger(), middleware.Recover())

	api := app.Group("/api")
	api.Get("/notes", func(context *vial.Context) error {
		store.mu.RLock()
		notes := append([]note(nil), store.notes...)
		store.mu.RUnlock()
		return context.JSON(http.StatusOK, notes)
	})

	api.Post("/notes", func(context *vial.Context) error {
		var request createNoteRequest
		if err := context.BindJSON(&request); err != nil {
			return err
		}
		if strings.TrimSpace(request.Title) == "" {
			return vial.BadRequest("title_required", "title is required")
		}

		store.mu.Lock()
		created := note{
			ID:    store.nextID,
			Title: request.Title,
			Body:  request.Body,
		}
		store.nextID++
		store.notes = append(store.notes, created)
		store.mu.Unlock()

		return context.JSON(http.StatusCreated, created)
	})
	return app
}
