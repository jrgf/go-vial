package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"

	"github.com/jrgf/go-vial"
	"github.com/jrgf/go-vial/config"
)

type applicationConfig struct {
	Environment string      `json:"environment" env:"VIAL_ENV"`
	HTTP        config.HTTP `json:"http"`
}

func (configuration *applicationConfig) Validate() error {
	if configuration.Environment == "" {
		return errors.New("environment is required")
	}
	return nil
}

func main() {
	configuration := applicationConfig{Environment: "development"}
	if err := config.Load(&configuration, config.OptionalFile("config.json")); err != nil {
		slog.Error("load configuration", "error", err)
		os.Exit(1)
	}

	if err := newApp(configuration).Run(context.Background(), configuration.HTTP.Address()); err != nil {
		slog.Error("application stopped with an error", "error", err)
		os.Exit(1)
	}
}

func newApp(configuration applicationConfig) *vial.App {
	app := vial.New()
	app.Get("/", func(context *vial.Context) error {
		return context.JSON(http.StatusOK, map[string]string{
			"environment": configuration.Environment,
			"address":     configuration.HTTP.Address(),
		})
	}, vial.RouteName("home"))
	return app
}
