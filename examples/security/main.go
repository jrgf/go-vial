package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"time"

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
	limit, err := middleware.RateLimit(middleware.RateLimitConfig{
		Requests: 3,
		Window:   time.Minute,
		MaxKeys:  1_000,
	})
	if err != nil {
		return nil, err
	}

	app := vial.New()
	app.Use(middleware.SecurityHeaders(), limit)
	app.Get("/", func(context *vial.Context) error {
		return context.JSON(http.StatusOK, map[string]string{"message": "request accepted"})
	}, vial.RouteName("home"))
	return app, nil
}
