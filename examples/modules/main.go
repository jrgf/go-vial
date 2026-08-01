package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"

	"github.com/jrgf/go-vial"
	"github.com/jrgf/go-vial/fault"
	"github.com/jrgf/go-vial/middleware"
)

type greetingModule struct {
	message string
}

func (greetingModule) Name() string {
	return "greetings"
}

func (module greetingModule) Register(registrar *vial.Registrar) error {
	registrar.Handle(http.MethodGet, "/", func(context *vial.Context) error {
		return context.JSON(http.StatusOK, map[string]string{"message": module.message})
	}, vial.RouteName("greetings.home"))
	registrar.Handle(http.MethodGet, "/greetings/{name}", func(context *vial.Context) error {
		var input struct {
			Name string `path:"name"`
		}
		if err := context.Bind(&input); err != nil {
			return err
		}
		if input.Name == "missing" {
			return fault.New(fault.NotFound, "greeting_not_found", "Greeting was not found")
		}
		return context.Text(http.StatusOK, "Hello, "+input.Name)
	}, vial.RouteName("greetings.show"))
	return nil
}

type healthModule struct{}

func (healthModule) Name() string {
	return "health"
}

func (healthModule) Register(registrar *vial.Registrar) error {
	registrar.Handle(http.MethodGet, "/health", func(context *vial.Context) error {
		return context.Text(http.StatusOK, "ok")
	}, vial.RouteName("health.check"))
	return nil
}

func main() {
	app, err := newApp()
	if err != nil {
		slog.Error("application configuration is invalid", "error", err)
		os.Exit(1)
	}
	if err := app.Run(context.Background(), ":8080"); err != nil {
		slog.Error("application stopped with an error", "error", err)
		os.Exit(1)
	}
}

func newApp() (*vial.App, error) {
	app := vial.New()
	app.Use(
		middleware.RequestID(),
		middleware.Logger(),
		middleware.Recover(),
	)
	if err := app.Register(
		greetingModule{message: "Hello from a Vial module"},
		healthModule{},
	); err != nil {
		return nil, err
	}
	return app, nil
}
