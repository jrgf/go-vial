package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestWebExample(t *testing.T) {
	app, err := newApp()
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		path        string
		contentType string
		body        string
	}{
		{"/", "text/html; charset=utf-8", "Hello, &lt;Vial&gt;"},
		{"/static/app.css", "text/css; charset=utf-8", "font-family"},
	}
	for _, test := range tests {
		t.Run(test.path, func(t *testing.T) {
			response := httptest.NewRecorder()
			app.ServeHTTP(response, httptest.NewRequest(http.MethodGet, test.path, nil))
			if response.Code != http.StatusOK {
				t.Fatalf("status=%d", response.Code)
			}
			if got := response.Header().Get("Content-Type"); got != test.contentType {
				t.Fatalf("content type=%q", got)
			}
			if !strings.Contains(response.Body.String(), test.body) {
				t.Fatalf("body=%q", response.Body.String())
			}
		})
	}
}

func TestWebFormValidationAndCSRF(t *testing.T) {
	app, err := newApp()
	if err != nil {
		t.Fatal(err)
	}

	response := httptest.NewRecorder()
	app.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "http://example.com/", nil))
	cookies := response.Result().Cookies()
	if response.Code != http.StatusOK || len(cookies) != 1 {
		t.Fatalf("status=%d cookies=%d", response.Code, len(cookies))
	}
	cookie := cookies[0]
	if !strings.Contains(response.Body.String(), `name="_csrf" value="`+cookie.Value+`"`) {
		t.Fatalf("csrf field missing from body=%q", response.Body.String())
	}

	tests := []struct {
		name       string
		value      string
		wantStatus int
		wantBody   string
	}{
		{name: "invalid", value: "   ", wantStatus: http.StatusBadRequest, wantBody: "Name is required"},
		{name: "valid", value: "Ada", wantStatus: http.StatusOK, wantBody: "Hello, Ada"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			body := url.Values{"_csrf": {cookie.Value}, "name": {test.value}}.Encode()
			request := httptest.NewRequest(http.MethodPost, "http://example.com/greet", strings.NewReader(body))
			request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			request.Header.Set("Origin", "http://example.com")
			request.AddCookie(cookie)
			response := httptest.NewRecorder()
			app.ServeHTTP(response, request)
			if response.Code != test.wantStatus || !strings.Contains(response.Body.String(), test.wantBody) {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
}
