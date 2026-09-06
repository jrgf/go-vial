package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/jrgf/go-vial"
	"github.com/jrgf/go-vial/middleware"
)

var csrfTestKey = []byte("0123456789abcdef0123456789abcdef")

func TestCSRFTokenAndCookiePolicy(t *testing.T) {
	app := csrfApp(t, middleware.CSRFConfig{Key: csrfTestKey})
	response := httptest.NewRecorder()
	app.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "https://example.com/", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	cookies := response.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("cookies=%d", len(cookies))
	}
	cookie := cookies[0]
	if cookie.Name != "__Host-vial_csrf" || !cookie.Secure || !cookie.HttpOnly ||
		cookie.SameSite != http.SameSiteStrictMode || cookie.Path != "/" || cookie.MaxAge <= 0 {
		t.Fatalf("cookie=%#v", cookie)
	}
	if token := response.Body.String(); token == "" || token != cookie.Value {
		t.Fatalf("token=%q cookie=%q", token, cookie.Value)
	}
	if got := response.Header().Values("Vary"); len(got) != 1 || got[0] != "Cookie" {
		t.Fatalf("vary=%v", got)
	}
}

func TestCSRFUnsafeRequests(t *testing.T) {
	app := csrfApp(t, middleware.CSRFConfig{Key: csrfTestKey})
	token, cookie := csrfToken(t, app)

	validForm := func() *http.Request {
		values := url.Values{"_csrf": {token}}
		request := httptest.NewRequest(http.MethodPost, "https://example.com/submit", strings.NewReader(values.Encode()))
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		request.Header.Set("Origin", "https://example.com")
		request.AddCookie(cookie)
		return request
	}

	tests := []struct {
		name       string
		request    func() *http.Request
		wantStatus int
	}{
		{name: "form token", request: validForm, wantStatus: http.StatusNoContent},
		{name: "header token", request: func() *http.Request {
			request := httptest.NewRequest(http.MethodPatch, "https://example.com/submit", strings.NewReader(`{"name":"Ada"}`))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Origin", "https://example.com")
			request.Header.Set("X-CSRF-Token", token)
			request.AddCookie(cookie)
			return request
		}, wantStatus: http.StatusNoContent},
		{name: "referer fallback", request: func() *http.Request {
			request := validForm()
			request.Header.Del("Origin")
			request.Header.Set("Referer", "https://example.com/form")
			return request
		}, wantStatus: http.StatusNoContent},
		{name: "missing token", request: func() *http.Request {
			request := validForm()
			request.Body = http.NoBody
			request.ContentLength = 0
			return request
		}, wantStatus: http.StatusForbidden},
		{name: "mismatched token", request: func() *http.Request {
			request := validForm()
			request.Header.Set("X-CSRF-Token", "wrong")
			return request
		}, wantStatus: http.StatusForbidden},
		{name: "tampered cookie", request: func() *http.Request {
			request := validForm()
			request.Header.Del("Cookie")
			request.AddCookie(&http.Cookie{Name: cookie.Name, Value: cookie.Value + "x"})
			request.Header.Set("X-CSRF-Token", cookie.Value+"x")
			return request
		}, wantStatus: http.StatusForbidden},
		{name: "cross origin", request: func() *http.Request {
			request := validForm()
			request.Header.Set("Origin", "https://attacker.example")
			return request
		}, wantStatus: http.StatusForbidden},
		{name: "origin suffix", request: func() *http.Request {
			request := validForm()
			request.Header.Set("Origin", "https://example.com.attacker.example")
			return request
		}, wantStatus: http.StatusForbidden},
		{name: "missing origin and referer", request: func() *http.Request {
			request := validForm()
			request.Header.Del("Origin")
			return request
		}, wantStatus: http.StatusForbidden},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			app.ServeHTTP(response, test.request())
			if response.Code != test.wantStatus {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
}

func TestCSRFSafeMethodsDoNotRequireOrigin(t *testing.T) {
	app := csrfApp(t, middleware.CSRFConfig{Key: csrfTestKey})
	for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodOptions} {
		t.Run(method, func(t *testing.T) {
			response := httptest.NewRecorder()
			app.ServeHTTP(response, httptest.NewRequest(method, "https://example.com/", nil))
			cookies := response.Result().Cookies()
			if response.Code == http.StatusForbidden || len(cookies) != 1 {
				t.Fatalf("status=%d cookies=%d", response.Code, len(cookies))
			}
		})
	}
}

func TestCSRFTrustedOriginAndConfiguration(t *testing.T) {
	app := csrfApp(t, middleware.CSRFConfig{
		Key:                             csrfTestKey,
		TrustedOrigins:                  []string{"https://public.example"},
		DangerouslyAllowInsecureCookies: true,
	})
	token, cookie := csrfToken(t, app)
	request := httptest.NewRequest(http.MethodDelete, "http://127.0.0.1/submit", nil)
	request.Header.Set("Origin", "https://public.example")
	request.Header.Set("X-CSRF-Token", token)
	request.AddCookie(cookie)
	response := httptest.NewRecorder()
	app.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if cookie.Name != "vial_csrf" || cookie.Secure {
		t.Fatalf("development cookie=%#v", cookie)
	}

	for _, config := range []middleware.CSRFConfig{
		{},
		{Key: []byte("short")},
		{Key: csrfTestKey, TrustedOrigins: []string{"ftp://example.com"}},
		{Key: csrfTestKey, TrustedOrigins: []string{"https://example.com/path"}},
	} {
		if _, err := middleware.CSRF(config); err == nil {
			t.Fatalf("expected configuration error for %#v", config)
		}
	}
}

func TestCSRFInsecureCookiesAreLoopbackOnly(t *testing.T) {
	app := csrfApp(t, middleware.CSRFConfig{
		Key:                             csrfTestKey,
		DangerouslyAllowInsecureCookies: true,
	})
	tests := []struct {
		name       string
		host       string
		forwarded  string
		wantStatus int
	}{
		{name: "IPv4 loopback", host: "127.0.0.1:8080", wantStatus: http.StatusOK},
		{name: "IPv6 loopback", host: "[::1]:8080", wantStatus: http.StatusOK},
		{name: "unspecified", host: "0.0.0.0:8080", wantStatus: http.StatusInternalServerError},
		{name: "public", host: "203.0.113.10", wantStatus: http.StatusInternalServerError},
		{name: "proxy headers are untrusted", host: "public.example", forwarded: "127.0.0.1", wantStatus: http.StatusInternalServerError},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "http://"+test.host+"/", nil)
			if test.forwarded != "" {
				request.Header.Set("X-Forwarded-Host", test.forwarded)
				request.Header.Set("Forwarded", "host="+test.forwarded)
			}
			response := httptest.NewRecorder()
			app.ServeHTTP(response, request)
			if response.Code != test.wantStatus {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			if test.wantStatus != http.StatusOK && len(response.Result().Cookies()) != 0 {
				t.Fatal("insecure cookie was issued to a non-loopback host")
			}
		})
	}
}

func csrfApp(t *testing.T, config middleware.CSRFConfig) *vial.App {
	t.Helper()
	protection, err := middleware.CSRF(config)
	if err != nil {
		t.Fatal(err)
	}
	app := vial.New()
	app.Use(protection)
	app.Get("/", func(context *vial.Context) error {
		return context.Text(http.StatusOK, middleware.CSRFToken(context))
	})
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		app.Handle(method, "/submit", func(context *vial.Context) error {
			return context.NoContent(http.StatusNoContent)
		})
	}
	return app
}

func csrfToken(t *testing.T, app *vial.App) (string, *http.Cookie) {
	t.Helper()
	response := httptest.NewRecorder()
	app.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "http://127.0.0.1/", nil))
	cookies := response.Result().Cookies()
	if response.Code != http.StatusOK || len(cookies) != 1 {
		t.Fatalf("status=%d cookies=%d body=%s", response.Code, len(cookies), response.Body.String())
	}
	return response.Body.String(), cookies[0]
}
