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
	app := newApp()
	address := os.Getenv("ADDR")
	if address == "" {
		address = ":8080"
	}

	if err := app.Run(context.Background(), address); err != nil {
		slog.Error("application stopped with an error", "error", err)
		os.Exit(1)
	}
}

func newApp() *vial.App {
	app := vial.New(
		vial.WithDisallowUnknownJSONFields(true),
	)

	app.Use(
		middleware.RequestID(),
		middleware.Logger(),
		middleware.Recover(),
	)

	app.Get("/", func(context *vial.Context) error {
		return context.JSON(http.StatusOK, map[string]string{
			"message": "Hello from vial",
		})
	})

	app.Get("/users/{id}", func(context *vial.Context) error {
		return context.JSON(http.StatusOK, map[string]string{
			"id": context.Param("id"),
		})
	})

	app.Get("/search", func(context *vial.Context) error {
		return context.JSON(http.StatusOK, map[string]string{
			"query": context.Query("q"),
		})
	})
	return app
}
