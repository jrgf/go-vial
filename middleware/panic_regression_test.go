package middleware

import (
	"bytes"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jrgf/go-vial"
)

func TestUnrecoveredPanicObservations(t *testing.T) {
	var log bytes.Buffer
	metrics := &HTTPMetrics{}
	app := vial.New(vial.WithLogger(slog.New(slog.NewTextHandler(&log, nil))))
	app.Use(Logger(), metrics.Middleware())
	app.Get("/", func(*vial.Context) error { panic("boom") })
	func() {
		defer func() {
			if recover() != "boom" {
				t.Error("panic did not propagate")
			}
		}()
		app.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
	}()
	if !strings.Contains(log.String(), "status=500") || !strings.Contains(log.String(), "level=ERROR") || !strings.Contains(log.String(), "error=") {
		t.Errorf("log=%s", log.String())
	}
	snapshot := metrics.snapshot()
	if len(snapshot) != 1 || snapshot[0].key.status != 500 || metrics.active.Load() != 0 {
		t.Errorf("metrics=%+v active=%d", snapshot, metrics.active.Load())
	}
}
