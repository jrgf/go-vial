package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"

	"github.com/jrgf/go-vial"
)

func main() {
	if err := newApp().Run(context.Background(), ":8080"); err != nil {
		slog.Error("application stopped with an error", "error", err)
		os.Exit(1)
	}
}

func newApp() *vial.App {
	app := vial.New()
	app.Post("/upload", func(context *vial.Context) error {
		var form struct {
			Title string `form:"title"`
		}
		if err := context.BindForm(&form); err != nil {
			return err
		}

		file, header, err := context.FormFile("file")
		if err != nil {
			return err
		}
		if err := file.Close(); err != nil {
			return err
		}

		return context.JSON(http.StatusCreated, map[string]any{
			"title":    form.Title,
			"filename": header.Filename,
			"size":     header.Size,
		})
	})
	return app
}
