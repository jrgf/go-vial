package testkit_test

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jrgf/go-vial"
	"github.com/jrgf/go-vial/testkit"
)

func TestServerRunsLifecycleAndHTTPRequests(t *testing.T) {
	var started atomic.Bool
	var stopped atomic.Bool
	app := vial.New()
	app.OnStart(func(context.Context) error {
		started.Store(true)
		return nil
	})
	app.OnStop(func(context.Context) error {
		stopped.Store(true)
		return nil
	})
	app.Post("/sessions", func(context *vial.Context) error {
		var input struct {
			Name string `json:"name"`
		}
		if err := context.BindJSON(&input); err != nil {
			return err
		}
		http.SetCookie(context.Response(), &http.Cookie{Name: "session", Value: input.Name, Path: "/"})
		return context.JSON(http.StatusCreated, input)
	})
	app.Get("/sessions", func(context *vial.Context) error {
		cookie, err := context.Request().Cookie("session")
		if err != nil {
			return err
		}
		return context.JSON(http.StatusOK, map[string]string{"name": cookie.Value})
	})

	t.Run("running", func(t *testing.T) {
		server := testkit.Start(t, app)
		if !started.Load() {
			t.Fatal("startup hook was not run")
		}

		created := server.JSON(http.MethodPost, "/sessions", map[string]string{"name": "vial"})
		created.RequireStatus(http.StatusCreated)
		var payload map[string]string
		created.Decode(&payload)
		if payload["name"] != "vial" {
			t.Fatalf("unexpected response: %#v", payload)
		}

		response := server.Do(server.NewRequest(http.MethodGet, "/sessions", nil))
		response.RequireStatus(http.StatusOK)
		response.Decode(&payload)
		if payload["name"] != "vial" {
			t.Fatalf("cookie was not preserved: %#v", payload)
		}
	})

	if !stopped.Load() {
		t.Fatal("shutdown hook was not run")
	}
}

func TestMultipartAndFaultResponse(t *testing.T) {
	app := vial.New()
	app.Post("/upload", func(context *vial.Context) error {
		var form struct {
			Title string   `form:"title"`
			Tags  []string `form:"tag"`
		}
		if err := context.BindForm(&form); err != nil {
			return err
		}
		file, header, err := context.FormFile("file")
		if err != nil {
			return err
		}
		content, readErr := io.ReadAll(file)
		if err := file.Close(); err != nil {
			return err
		}
		if readErr != nil {
			return readErr
		}
		return context.JSON(http.StatusCreated, map[string]any{
			"title":    form.Title,
			"tags":     form.Tags,
			"filename": header.Filename,
			"content":  string(content),
		})
	})
	server := testkit.Start(t, app)

	response := server.Multipart(
		http.MethodPost,
		"/upload",
		url.Values{"title": {"Example"}, "tag": {"go", "web"}},
		testkit.File{Field: "file", Name: "example.txt", Body: strings.NewReader("hello vial")},
	)
	response.RequireStatus(http.StatusCreated)
	var result struct {
		Title    string   `json:"title"`
		Tags     []string `json:"tags"`
		Filename string   `json:"filename"`
		Content  string   `json:"content"`
	}
	response.Decode(&result)
	if result.Title != "Example" || len(result.Tags) != 2 || result.Filename != "example.txt" || result.Content != "hello vial" {
		t.Fatalf("unexpected multipart response: %#v", result)
	}

	missing := server.Multipart(http.MethodPost, "/upload", nil)
	missing.RequireStatus(http.StatusBadRequest)
	if got := missing.Fault(); got.Code != "missing_file" {
		t.Fatalf("unexpected fault: %#v", got)
	}
}
