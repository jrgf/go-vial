package middleware

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jrgf/go-vial"
)

func TestCoverageCORSBranches(t *testing.T) {
	values, normalized, err := normalizeCORSValues("header", []string{"X-Test", "x-test"}, false)
	if err != nil || len(values) != 1 || len(normalized) != 1 {
		t.Fatalf("normalize duplicates: %v %#v %#v", err, values, normalized)
	}

	cors, err := CORS(CORSConfig{AllowedOrigins: []string{"https://example.com"}, AllowedMethods: []string{http.MethodGet}})
	if err != nil {
		t.Fatal(err)
	}
	app := vial.New()
	app.Use(cors)
	app.Post("/", func(context *vial.Context) error { return context.NoContent(http.StatusNoContent) })
	request := httptest.NewRequest(http.MethodPost, "http://server/", nil)
	request.Header.Set("Origin", "https://example.com")
	response := httptest.NewRecorder()
	app.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d", response.Code)
	}
}

func TestCoverageCSRFHelpers(t *testing.T) {
	if !csrfLoopbackHost("localhost") {
		t.Fatal("localhost must be loopback")
	}
	for value, want := range map[string]string{
		"http://example.com:80": "http://example.com",
		"https://[::1]":         "https://[::1]",
	} {
		if got, err := normalizeOrigin(value, false); err != nil || got != want {
			t.Fatalf("normalizeOrigin(%q) = %q, %v", value, got, err)
		}
	}
	if _, err := normalizeOrigin("http://:80", false); err == nil {
		t.Fatal("expected missing host error")
	}

	policy, err := newCSRFPolicy(CSRFConfig{Key: bytes.Repeat([]byte("k"), 32), DangerouslyAllowInsecureCookies: true})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "http://example.com/", nil)
	request.Header.Set("Origin", "%")
	if policy.allowsRequestOrigin(request) {
		t.Fatal("malformed origin was allowed")
	}
}

func TestCoverageCSRFCookieAndErrors(t *testing.T) {
	middlewareValue, err := CSRF(CSRFConfig{Key: bytes.Repeat([]byte("k"), 32), DangerouslyAllowInsecureCookies: true})
	if err != nil {
		t.Fatal(err)
	}
	app := vial.New()
	app.Use(middlewareValue)
	app.Get("/", func(context *vial.Context) error { return context.Text(http.StatusOK, CSRFToken(context)) })
	app.Post("/", func(context *vial.Context) error { return context.NoContent(http.StatusNoContent) })

	first := httptest.NewRecorder()
	app.ServeHTTP(first, httptest.NewRequest(http.MethodGet, "http://localhost/", nil))
	cookie := first.Result().Cookies()[0]
	secondRequest := httptest.NewRequest(http.MethodGet, "http://localhost/", nil)
	secondRequest.AddCookie(cookie)
	second := httptest.NewRecorder()
	app.ServeHTTP(second, secondRequest)
	if second.Code != http.StatusOK || second.Body.String() != first.Body.String() {
		t.Fatalf("cookie token was not reused: %d %q", second.Code, second.Body.String())
	}

	badType := httptest.NewRequest(http.MethodPost, "http://localhost/", strings.NewReader("_csrf=x"))
	badType.Header.Set("Origin", "http://localhost")
	badType.Header.Set("Content-Type", "bad;")
	badType.AddCookie(cookie)
	badResponse := httptest.NewRecorder()
	app.ServeHTTP(badResponse, badType)
	if badResponse.Code != http.StatusForbidden {
		t.Fatalf("bad content type status = %d", badResponse.Code)
	}
	badForm := httptest.NewRequest(http.MethodPost, "http://localhost/", strings.NewReader("_csrf=%ZZ"))
	badForm.Header.Set("Origin", "http://localhost")
	badForm.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	badForm.AddCookie(cookie)
	badFormResponse := httptest.NewRecorder()
	app.ServeHTTP(badFormResponse, badForm)
	if badFormResponse.Code != http.StatusBadRequest {
		t.Fatalf("bad form status = %d", badFormResponse.Code)
	}

	original := randomRead
	randomRead = func([]byte) (int, error) { return 0, errors.New("random failed") }
	t.Cleanup(func() { randomRead = original })
	failure := httptest.NewRecorder()
	app.ServeHTTP(failure, httptest.NewRequest(http.MethodGet, "http://localhost/", nil))
	if failure.Code != http.StatusInternalServerError {
		t.Fatalf("random failure status = %d", failure.Code)
	}
	if id := newRequestID(); id == "" {
		t.Fatal("fallback request id is empty")
	}
}
