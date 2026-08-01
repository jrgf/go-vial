package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"

	"github.com/jrgf/go-vial"
	"github.com/jrgf/go-vial/middleware"
)

func main() {
	app, err := newApp()
	if err != nil {
		slog.Error("application configuration is invalid", "error", err)
		os.Exit(1)
	}
	address := os.Getenv("ADDR")
	if address == "" {
		address = ":8080"
	}

	if err := app.Run(context.Background(), address); err != nil {
		slog.Error("application stopped with an error", "error", err)
		os.Exit(1)
	}
}

func newApp() (*vial.App, error) {
	app := vial.New(
		vial.WithDisallowUnknownJSONFields(true),
	)
	cors, err := middleware.CORS(middleware.CORSConfig{
		AllowedOrigins: []string{"http://localhost:3000"},
		AllowedHeaders: []string{"Content-Type"},
	})
	if err != nil {
		return nil, err
	}

	app.Use(
		middleware.RequestID(),
		middleware.Logger(),
		middleware.Recover(),
		cors,
	)

	app.Get("/", func(context *vial.Context) error {
		return context.JSON(http.StatusOK, map[string]string{
			"message": "Hello from vial",
		})
	}, vial.RouteName("home"))

	app.Get("/users/{id}", func(context *vial.Context) error {
		return context.JSON(http.StatusOK, map[string]string{
			"id": context.Param("id"),
		})
	}, vial.RouteName("users.show"))

	app.Get("/search", func(context *vial.Context) error {
		return context.JSON(http.StatusOK, map[string]string{
			"query": context.Query("q"),
		})
	}, vial.RouteName("search"))

	app.Post("/submit", func(context *vial.Context) error {
		return context.NoContent(http.StatusNoContent)
	}, vial.RouteName("submit"))
	return app, nil
}
