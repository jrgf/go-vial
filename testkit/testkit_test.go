package testkit_test

import (
	"context"
	"net/http"
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
