package middleware

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jrgf/go-vial"
)

func TestOpenMetricsCounterFamily(t *testing.T) {
	m := &HTTPMetrics{}
	app := vial.New()
	app.Use(m.Middleware())
	app.Get("/", m.Handler)
	w := httptest.NewRecorder()
	app.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	if !strings.Contains(w.Body.String(), "# TYPE vial_http_requests counter\n") || !strings.Contains(w.Body.String(), "# HELP vial_http_requests ") || strings.Contains(w.Body.String(), "# TYPE vial_http_requests_total") {
		t.Fatal(w.Body.String())
	}
}
