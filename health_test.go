package vial_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jrgf/go-vial"
	"github.com/jrgf/go-vial/testkit"
)

func TestHealthIsDependencyFree(t *testing.T) {
	app := vial.New()
	app.Health("/healthz")
	response := httptest.NewRecorder()
	app.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if response.Code != http.StatusNoContent {
		t.Fatalf("liveness status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestReadinessChecksTimeoutAndHideDetails(t *testing.T) {
	app := vial.New(
		vial.WithHealthCheckTimeout(25*time.Millisecond),
		vial.WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))),
	)
	app.Readiness("/readyz", func(context.Context) error { return nil })
	app.Readiness("/slow", func(contextValue context.Context) error {
		<-contextValue.Done()
		return contextValue.Err()
	})
	app.Readiness("/failed", func(context.Context) error { return errors.New("secret database detail") })
	app.Readiness("/panic", func(context.Context) error { panic("secret panic detail") })

	var shutdownStatus atomic.Int64
	app.OnStop(func(context.Context) error {
		response := httptest.NewRecorder()
		app.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/readyz", nil))
		shutdownStatus.Store(int64(response.Code))
		return nil
	})
	server := testkit.Start(t, app)

	for _, test := range []struct {
		path       string
		wantStatus int
	}{
		{path: "/readyz", wantStatus: http.StatusNoContent},
		{path: "/slow", wantStatus: http.StatusServiceUnavailable},
		{path: "/failed", wantStatus: http.StatusServiceUnavailable},
		{path: "/panic", wantStatus: http.StatusServiceUnavailable},
	} {
		response, err := server.Client.Get(server.URL + test.path)
		if err != nil {
			t.Fatal(err)
		}
		body, readErr := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if readErr != nil || response.StatusCode != test.wantStatus {
			t.Fatalf("%s status=%d body=%q err=%v", test.path, response.StatusCode, body, readErr)
		}
		if strings.Contains(string(body), "secret") {
			t.Fatalf("%s leaked check details: %s", test.path, body)
		}
	}

	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
	if got := shutdownStatus.Load(); got != http.StatusServiceUnavailable {
		t.Fatalf("readiness during shutdown=%d", got)
	}
}
