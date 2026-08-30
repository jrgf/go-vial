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
	app := newApp(slog.Default())
	address := os.Getenv("ADDR")
	if address == "" {
		address = ":8080"
	}
	if err := app.Run(context.Background(), address); err != nil {
		slog.Error("application stopped with an error", "error", err)
		os.Exit(1)
	}
}

func newApp(logger *slog.Logger) *vial.App {
	metrics := &middleware.HTTPMetrics{}
	app := vial.New(vial.WithLogger(logger))
	app.Use(
		middleware.RequestID(),
		middleware.TraceContext(middleware.W3CTraceID),
		metrics.Middleware(),
		middleware.Logger(),
		middleware.Recover(),
	)
	app.Get("/", func(context *vial.Context) error {
		return context.JSON(http.StatusOK, map[string]string{
			"request_id": middleware.RequestIDFromContext(context),
			"trace_id":   middleware.TraceIDFromContext(context),
		})
	}, vial.RouteName("home"))
	app.Get("/metrics", metrics.Handler, vial.RouteName("metrics"))
	return app
}
