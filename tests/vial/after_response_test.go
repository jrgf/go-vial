package vial_test

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jrgf/go-vial"
)

func TestAfterResponseSeesRenderedResponse(t *testing.T) {
	app := vial.New()
	var status int
	var bytesWritten int64
	app.Use(func(next vial.Handler) vial.Handler {
		return func(context *vial.Context) error {
			if err := context.AfterResponse(func() {
				status = context.Status()
				bytesWritten = context.BytesWritten()
			}); err != nil {
				return err
			}
			return next(context)
		}
	})
	app.Get("/", func(*vial.Context) error {
		return vial.BadRequest("bad", "bad request")
	})

	response := httptest.NewRecorder()
	app.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
	if status != http.StatusBadRequest || bytesWritten != int64(response.Body.Len()) || bytesWritten == 0 {
		t.Fatalf("after response: status=%d bytes=%d body=%d", status, bytesWritten, response.Body.Len())
	}
}

func TestAfterResponseValidationAndPanicIsolation(t *testing.T) {
	var logs bytes.Buffer
	app := vial.New(vial.WithLogger(slog.New(slog.NewTextHandler(&logs, nil))))
	app.Use(func(next vial.Handler) vial.Handler {
		return func(context *vial.Context) error {
			if err := context.AfterResponse(nil); err == nil {
				t.Error("nil hook succeeded")
			}
			if err := context.AfterResponse(func() { panic("hook failure") }); err != nil {
				t.Fatal(err)
			}
			return next(context)
		}
	})
	app.Get("/", func(context *vial.Context) error {
		return context.NoContent(http.StatusNoContent)
	})

	response := httptest.NewRecorder()
	app.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
	if response.Code != http.StatusNoContent || !strings.Contains(logs.String(), "hook failure") {
		t.Fatalf("response status=%d logs=%s", response.Code, logs.String())
	}
}
