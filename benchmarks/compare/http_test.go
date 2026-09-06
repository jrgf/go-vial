package compare

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	vial "github.com/jrgf/go-vial"
)

var frameworks = []string{"Vial", "Fiber", "net_http"}

var requests = []struct {
	name, path, body, contentType string
}{
	{"text", "/text", "hello", "text/plain; charset=utf-8"},
	{"json", "/json", "{\"message\":\"hello\",\"count\":3}\n", "application/json; charset=utf-8"},
	{"parameter", "/users/42", "42", "text/plain; charset=utf-8"},
}

type message struct {
	Message string `json:"message"`
	Count   int    `json:"count"`
}

func encodeJSON(value any) ([]byte, error) {
	body, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return append(body, '\n'), nil
}

// BenchmarkHTTP uses the same HTTP/1.1 client, connections, response bytes,
// encoding/json codec, and Go scheduler for every server. The client and server
// share a process; use an external load generator for capacity and tail latency.
func BenchmarkHTTP(b *testing.B) {
	for _, framework := range frameworks {
		for _, request := range requests {
			b.Run(framework+"/"+request.name, func(b *testing.B) {
				base, client := startServer(b, framework)
				checkResponse(b, client, base+request.path, request.body, request.contentType)
				b.ReportAllocs()
				b.ResetTimer()
				b.RunParallel(func(pb *testing.PB) {
					for pb.Next() {
						checkResponse(b, client, base+request.path, request.body, request.contentType)
					}
				})
			})
		}
	}
}

func TestEquivalentResponses(t *testing.T) {
	for _, framework := range frameworks {
		t.Run(framework, func(t *testing.T) {
			base, client := startServer(t, framework)
			for _, request := range requests {
				checkResponse(t, client, base+request.path, request.body, request.contentType)
			}
		})
	}
}

func checkResponse(t testing.TB, client *http.Client, url, wantBody, wantType string) {
	t.Helper()
	response, err := client.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	body, readErr := io.ReadAll(response.Body)
	closeErr := response.Body.Close()
	if readErr != nil || closeErr != nil || response.StatusCode != http.StatusOK || string(body) != wantBody || response.Header.Get("Content-Type") != wantType {
		t.Fatalf("response: status=%d body=%q type=%q read=%v close=%v", response.StatusCode, body, response.Header.Get("Content-Type"), readErr, closeErr)
	}
}

func startServer(t testing.TB, framework string) (string, *http.Client) {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	var shutdown func(context.Context) error
	if framework == "Fiber" {
		app := fiber.New(fiber.Config{
			JSONEncoder: encodeJSON,
			ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second,
			IdleTimeout: 30 * time.Second,
		})
		app.Get("/text", func(c fiber.Ctx) error { return c.SendString("hello") })
		app.Get("/json", func(c fiber.Ctx) error { return c.JSON(message{"hello", 3}) })
		app.Get("/users/:id", func(c fiber.Ctx) error { return c.SendString(c.Params("id")) })
		shutdown = app.ShutdownWithContext
		go func() { done <- app.Listener(listener, fiber.ListenConfig{DisableStartupMessage: true}) }()
	} else {
		var handler http.Handler
		if framework == "Vial" {
			app := vial.New()
			app.Get("/text", func(c *vial.Context) error { return c.Text(http.StatusOK, "hello") })
			app.Get("/json", func(c *vial.Context) error { return c.JSON(http.StatusOK, message{"hello", 3}) })
			app.Get("/users/{id}", func(c *vial.Context) error { return c.Text(http.StatusOK, c.Param("id")) })
			if err := app.Build(); err != nil {
				_ = listener.Close()
				t.Fatal(err)
			}
			handler = app
		} else {
			mux := http.NewServeMux()
			mux.HandleFunc("GET /text", func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/plain; charset=utf-8")
				_, _ = io.WriteString(w, "hello")
			})
			mux.HandleFunc("GET /json", func(w http.ResponseWriter, _ *http.Request) {
				body, err := encodeJSON(message{"hello", 3})
				if err != nil {
					http.Error(w, "encode error", http.StatusInternalServerError)
					return
				}
				w.Header().Set("Content-Type", "application/json; charset=utf-8")
				_, _ = w.Write(body)
			})
			mux.HandleFunc("GET /users/{id}", func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/plain; charset=utf-8")
				_, _ = io.WriteString(w, r.PathValue("id"))
			})
			handler = mux
		}
		server := &http.Server{
			Handler: handler, ReadHeaderTimeout: 5 * time.Second,
			ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second,
			IdleTimeout: 30 * time.Second,
		}
		shutdown = server.Shutdown
		go func() { done <- server.Serve(listener) }()
	}
	transport := &http.Transport{MaxIdleConns: 128, MaxIdleConnsPerHost: 128, MaxConnsPerHost: 128}
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	t.Cleanup(func() {
		transport.CloseIdleConnections()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := shutdown(ctx); err != nil {
			t.Errorf("shutdown: %v", err)
			_ = listener.Close()
		}
		select {
		case err := <-done:
			if err != nil && !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, net.ErrClosed) {
				t.Errorf("serve: %v", err)
			}
		case <-ctx.Done():
			t.Error("server did not stop")
		}
	})
	return "http://" + listener.Addr().String(), client
}
