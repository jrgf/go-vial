package middleware

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jrgf/go-vial"
)

func TestSecurityHeaders(t *testing.T) {
	app := vial.New()
	app.Use(SecurityHeaders())
	app.Get("/", func(context *vial.Context) error {
		context.Response().Header().Set("Content-Security-Policy", "default-src 'none'")
		return errors.New("failure")
	})

	request := httptest.NewRequest(http.MethodGet, "https://example.test/", nil)
	response := httptest.NewRecorder()
	app.ServeHTTP(response, request)

	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", response.Code)
	}
	want := map[string]string{
		"Content-Security-Policy":   "default-src 'none'",
		"Referrer-Policy":           "no-referrer",
		"Strict-Transport-Security": "max-age=31536000",
		"X-Content-Type-Options":    "nosniff",
		"X-Frame-Options":           "DENY",
	}
	for name, value := range want {
		if got := response.Header().Get(name); got != value {
			t.Errorf("%s = %q, want %q", name, got, value)
		}
	}

	plain := httptest.NewRecorder()
	app.ServeHTTP(plain, httptest.NewRequest(http.MethodGet, "http://example.test/", nil))
	if got := plain.Header().Get("Strict-Transport-Security"); got != "" {
		t.Fatalf("HTTP response contains HSTS %q", got)
	}
}
