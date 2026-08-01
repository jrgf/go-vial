package main

import (
	"context"
	"crypto/rand"
	"embed"
	"errors"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"strings"

	"github.com/jrgf/go-vial"
	"github.com/jrgf/go-vial/fault"
	"github.com/jrgf/go-vial/middleware"
	"github.com/jrgf/go-vial/render"
)

//go:embed templates/*.html static/*
var webFS embed.FS

type greetingForm struct {
	Name string `form:"name"`
}

func (form *greetingForm) Validate() error {
	form.Name = strings.TrimSpace(form.Name)
	if form.Name != "" {
		return nil
	}
	err := fault.New(fault.InvalidArgument, "name_required", "Name is required")
	err.Fields = map[string]string{"name": "Name is required"}
	return err
}

type homePage struct {
	Title     string
	Greeting  string
	CSRFToken string
	Form      greetingForm
	Errors    map[string]string
	Message   string
}

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
	csrfKey := make([]byte, 32)
	if _, err := rand.Read(csrfKey); err != nil {
		return nil, err
	}
	csrf, err := middleware.CSRF(middleware.CSRFConfig{
		Key:           csrfKey,
		AllowInsecure: os.Getenv("VIAL_ALLOW_INSECURE_COOKIE") == "1",
	})
	if err != nil {
		return nil, err
	}

	app := vial.New()
	pages := app.Group("/")
	pages.Use(csrf)
	pages.Get("/", func(context *vial.Context) error {
		return renderHome(views, context, http.StatusOK, homePage{Greeting: "<Vial>"})
	}, vial.RouteName("home"))
	pages.Post("/greet", func(context *vial.Context) error {
		var form greetingForm
		if err := context.BindForm(&form); err != nil {
			var faultErr *fault.Error
			if errors.As(err, &faultErr) && faultErr.Kind == fault.InvalidArgument {
				return renderHome(views, context, http.StatusBadRequest, homePage{
					Greeting: "<Vial>",
					Form:     form,
					Errors:   faultErr.Fields,
				})
			}
			return err
		}
		return renderHome(views, context, http.StatusOK, homePage{
			Greeting: form.Name,
			Form:     form,
			Message:  "Form accepted",
		})
	}, vial.RouteName("greet"))
	app.HandleHTTP(
		"GET /static/",
		http.StripPrefix("/static/", http.FileServerFS(assets)),
		vial.RouteName("static"),
	)
	return app, nil
}

func renderHome(views *render.Renderer, context *vial.Context, status int, page homePage) error {
	page.Title = "Vial web example"
	page.CSRFToken = middleware.CSRFToken(context)
	return views.HTML(context, status, "layout", page)
}
