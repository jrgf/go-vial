package main

import (
	"context"
	"embed"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"os"

	"github.com/jrgf/go-vial"
	"github.com/jrgf/go-vial/render"
)

//go:embed templates/*.html static/*
var webFS embed.FS

func main() {
	app, err := newApp()
	if err != nil {
		slog.Error("build application", "error", err)
		os.Exit(1)
	}
	if err := app.Run(context.Background(), ":8080"); err != nil {
		slog.Error("application stopped with an error", "error", err)
		os.Exit(1)
	}
}

func newApp() (*vial.App, error) {
	parsed, err := template.New("views").ParseFS(webFS, "templates/*.html")
	if err != nil {
		return nil, err
	}
	assets, err := fs.Sub(webFS, "static")
	if err != nil {
		return nil, err
	}

	views := render.New(parsed)
	app := vial.New()
	app.Get("/", func(context *vial.Context) error {
		return views.HTML(context, http.StatusOK, "layout", struct {
			Title string
			Name  string
		}{Title: "Vial web example", Name: "<Vial>"})
	}, vial.RouteName("home"))
	app.HandleHTTP(
		"GET /static/",
		http.StripPrefix("/static/", http.FileServerFS(assets)),
		vial.RouteName("static"),
	)
	return app, nil
}
