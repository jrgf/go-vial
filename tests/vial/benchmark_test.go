package vial_test

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jrgf/go-vial"
)

type benchmarkInput struct {
	Name string `json:"name" validate:"required"`
	Age  uint8  `json:"age"`
}

func (input *benchmarkInput) Validate() error {
	if input.Name == "" {
		return errors.New("name is required")
	}
	return nil
}

func BenchmarkHTTP(b *testing.B) {
	b.Run("text", func(b *testing.B) {
		app := vial.New()
		app.Get("/", func(context *vial.Context) error { return context.Text(http.StatusOK, "hello") })
		benchmarkRequests(b, app, http.MethodGet, "/", nil, "", http.StatusOK)
	})
	b.Run("json", func(b *testing.B) {
		app := vial.New()
		app.Get("/", func(context *vial.Context) error {
			return context.JSON(http.StatusOK, map[string]any{"message": "hello", "count": 3})
		})
		benchmarkRequests(b, app, http.MethodGet, "/", nil, "", http.StatusOK)
	})
	b.Run("bind-json-validate", func(b *testing.B) {
		app := vial.New()
		app.Post("/", func(context *vial.Context) error {
			var input benchmarkInput
			if err := context.BindJSON(&input); err != nil {
				return err
			}
			return context.NoContent(http.StatusNoContent)
		})
		benchmarkRequests(b, app, http.MethodPost, "/", []byte(`{"name":"Ada","age":36}`), "application/json", http.StatusNoContent)
	})
	b.Run("static", func(b *testing.B) {
		app := vial.New()
		app.HandleHTTP("GET /asset.txt", http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			_, _ = writer.Write([]byte("asset"))
		}))
		benchmarkRequests(b, app, http.MethodGet, "/asset.txt", nil, "", http.StatusOK)
	})
	b.Run("parameter", func(b *testing.B) {
		app := vial.New()
		app.Get("/users/{id}", func(context *vial.Context) error { return context.Text(http.StatusOK, context.Param("id")) })
		benchmarkRequests(b, app, http.MethodGet, "/users/42", nil, "", http.StatusOK)
	})
	for _, count := range []int{10, 100, 1000} {
		b.Run(fmt.Sprintf("routes-%d", count), func(b *testing.B) {
			app := vial.New()
			for index := range count {
				app.Get(fmt.Sprintf("/route/%d", index), func(context *vial.Context) error {
					return context.NoContent(http.StatusNoContent)
				})
			}
			benchmarkRequests(b, app, http.MethodGet, fmt.Sprintf("/route/%d", count-1), nil, "", http.StatusNoContent)
		})
	}
	for _, depth := range []int{1, 5, 10} {
		b.Run(fmt.Sprintf("middleware-%d", depth), func(b *testing.B) {
			app := vial.New()
			for range depth {
				app.Use(func(next vial.Handler) vial.Handler {
					return func(context *vial.Context) error { return next(context) }
				})
			}
			app.Get("/", func(context *vial.Context) error { return context.NoContent(http.StatusNoContent) })
			benchmarkRequests(b, app, http.MethodGet, "/", nil, "", http.StatusNoContent)
		})
	}
	b.Run("not-found", func(b *testing.B) {
		app := vial.New()
		benchmarkRequests(b, app, http.MethodGet, "/missing", nil, "", http.StatusNotFound)
	})
	b.Run("method-not-allowed", func(b *testing.B) {
		app := vial.New()
		app.Get("/", func(context *vial.Context) error { return context.NoContent(http.StatusNoContent) })
		benchmarkRequests(b, app, http.MethodPost, "/", nil, "", http.StatusMethodNotAllowed)
	})
	for _, size := range []int{256, 64 << 10} {
		b.Run(fmt.Sprintf("body-%d", size), func(b *testing.B) {
			app := vial.New()
			app.Post("/", func(context *vial.Context) error {
				var value map[string]string
				if err := context.BindJSON(&value); err != nil {
					return err
				}
				return context.NoContent(http.StatusNoContent)
			})
			body := []byte(`{"value":"` + strings.Repeat("x", size) + `"}`)
			benchmarkRequests(b, app, http.MethodPost, "/", body, "application/json", http.StatusNoContent)
		})
	}
	b.Run("multipart", func(b *testing.B) {
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		_ = writer.WriteField("title", "release")
		file, _ := writer.CreateFormFile("upload", "note.txt")
		_, _ = file.Write([]byte("hello vial"))
		_ = writer.Close()
		app := vial.New()
		app.Post("/", func(context *vial.Context) error {
			var form struct {
				Title string `form:"title"`
			}
			if err := context.BindForm(&form); err != nil {
				return err
			}
			file, _, err := context.FormFile("upload")
			if err != nil {
				return err
			}
			_ = file.Close()
			return context.NoContent(http.StatusNoContent)
		})
		benchmarkRequests(b, app, http.MethodPost, "/", body.Bytes(), writer.FormDataContentType(), http.StatusNoContent)
	})
	b.Run("sse-flush", func(b *testing.B) {
		app := vial.New()
		app.Get("/", func(context *vial.Context) error {
			context.Response().Header().Set("Content-Type", "text/event-stream")
			if _, err := context.Response().Write([]byte("data: ready\n\n")); err != nil {
				return err
			}
			return context.Flush()
		})
		benchmarkRequests(b, app, http.MethodGet, "/", nil, "", http.StatusOK)
	})
	b.Run("concurrent-slow", func(b *testing.B) {
		app := vial.New()
		app.Get("/", func(context *vial.Context) error {
			time.Sleep(time.Microsecond)
			return context.NoContent(http.StatusNoContent)
		})
		if err := app.Build(); err != nil {
			b.Fatal(err)
		}
		b.ReportAllocs()
		b.RunParallel(func(parallel *testing.PB) {
			for parallel.Next() {
				response := httptest.NewRecorder()
				app.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
				if response.Code != http.StatusNoContent {
					b.Fatalf("status = %d", response.Code)
				}
			}
		})
	})
}

func benchmarkRequests(b *testing.B, app *vial.App, method, path string, body []byte, contentType string, wantStatus int) {
	b.Helper()
	if err := app.Build(); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		request := httptest.NewRequest(method, path, bytes.NewReader(body))
		if contentType != "" {
			request.Header.Set("Content-Type", contentType)
		}
		response := httptest.NewRecorder()
		app.ServeHTTP(response, request)
		if response.Code != wantStatus {
			b.Fatalf("status = %d, want %d", response.Code, wantStatus)
		}
	}
}

// BenchmarkDispatch isolates concurrent handler overhead from request parsing
// and response recording. Each worker owns its request and response writer.
func BenchmarkDispatch(b *testing.B) {
	for _, framework := range []string{"Vial", "net_http"} {
		b.Run(framework, func(b *testing.B) {
			var handler http.Handler
			if framework == "Vial" {
				app := vial.New()
				app.Get("/users/{id}", func(c *vial.Context) error {
					return c.Text(http.StatusOK, c.Param("id"))
				})
				if err := app.Build(); err != nil {
					b.Fatal(err)
				}
				handler = app
			} else {
				mux := http.NewServeMux()
				mux.HandleFunc("GET /users/{id}", func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "text/plain; charset=utf-8")
					w.WriteHeader(http.StatusOK)
					_, _ = io.WriteString(w, r.PathValue("id"))
				})
				handler = mux
			}
			b.ReportAllocs()
			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				request := httptest.NewRequest(http.MethodGet, "/users/42", nil)
				response := &dispatchResponse{header: make(http.Header)}
				for pb.Next() {
					clear(response.header)
					response.status, response.bytes = 0, 0
					handler.ServeHTTP(response, request)
					if response.status != http.StatusOK || response.bytes != 2 {
						b.Fatalf("response: status=%d bytes=%d", response.status, response.bytes)
					}
				}
			})
		})
	}
}

type dispatchResponse struct {
	header http.Header
	status int
	bytes  int
}

func (w *dispatchResponse) Header() http.Header    { return w.header }
func (w *dispatchResponse) WriteHeader(status int) { w.status = status }
func (w *dispatchResponse) Write(p []byte) (int, error) {
	w.bytes += len(p)
	return len(p), nil
}
func (w *dispatchResponse) WriteString(s string) (int, error) {
	w.bytes += len(s)
	return len(s), nil
}
