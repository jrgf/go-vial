package vial_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jrgf/go-vial"
	"github.com/jrgf/go-vial/fault"
)

func TestFaultHTTPStatusMapping(t *testing.T) {
	tests := []struct {
		kind   fault.Kind
		status int
	}{
		{fault.InvalidArgument, http.StatusBadRequest},
		{fault.Unauthenticated, http.StatusUnauthorized},
		{fault.Forbidden, http.StatusForbidden},
		{fault.NotFound, http.StatusNotFound},
		{fault.Conflict, http.StatusConflict},
		{fault.RateLimited, http.StatusTooManyRequests},
		{fault.Unavailable, http.StatusServiceUnavailable},
		{fault.Internal, http.StatusInternalServerError},
		{fault.Kind(255), http.StatusInternalServerError},
	}

	for _, test := range tests {
		err := fmt.Errorf("service failed: %w", fault.New(test.kind, "", ""))
		if got := vial.StatusCode(err); got != test.status {
			t.Errorf("kind %d mapped to %d, want %d", test.kind, got, test.status)
		}
	}
}

func TestFaultHTTPResponseIncludesPublicFields(t *testing.T) {
	appErr := fault.New(fault.InvalidArgument, "invalid_user", "User is invalid")
	appErr.Fields = map[string]string{"email": "required"}
	appErr.Meta = map[string]any{"secret": "internal"}

	app := vial.New()
	app.Get("/", func(*vial.Context) error {
		return fmt.Errorf("create user: %w", appErr)
	})
	response := httptest.NewRecorder()
	app.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))

	var payload struct {
		Error struct {
			Code    string            `json:"code"`
			Message string            `json:"message"`
			Fields  map[string]string `json:"fields"`
		} `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusBadRequest || payload.Error.Code != "invalid_user" || payload.Error.Message != "User is invalid" {
		t.Fatalf("unexpected fault response: status=%d payload=%#v", response.Code, payload)
	}
	if payload.Error.Fields["email"] != "required" || strings.Contains(response.Body.String(), "internal") {
		t.Fatalf("unexpected public fault details: %s", response.Body.String())
	}
}

func TestInternalFaultResponseIsSanitized(t *testing.T) {
	app := vial.New()
	app.Get("/", func(*vial.Context) error {
		return fault.Wrap(fault.Internal, "database_failed", "sensitive database message", errors.New("sensitive cause"))
	})
	response := httptest.NewRecorder()
	app.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))

	if response.Code != http.StatusInternalServerError || !strings.Contains(response.Body.String(), `"code":"database_failed"`) {
		t.Fatalf("unexpected internal fault response: status=%d body=%s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "sensitive") {
		t.Fatalf("internal fault leaked details: %s", response.Body.String())
	}
}

func TestHTTPErrorOverridesNestedFault(t *testing.T) {
	httpErr := vial.NewHTTPError(http.StatusTeapot, "teapot", "Short and stout")
	httpErr.Cause = fault.New(fault.NotFound, "missing", "Missing")
	httpErr.Headers = http.Header{"X-Transport": []string{"http"}}

	app := vial.New()
	app.Get("/", func(*vial.Context) error { return httpErr })
	response := httptest.NewRecorder()
	app.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))

	if response.Code != http.StatusTeapot || response.Header().Get("X-Transport") != "http" || !strings.Contains(response.Body.String(), `"code":"teapot"`) {
		t.Fatalf("unexpected HTTP override: status=%d headers=%v body=%s", response.Code, response.Header(), response.Body.String())
	}
}

func TestTypedNilErrorsMapToInternalServerError(t *testing.T) {
	var httpErr *vial.HTTPError
	var appErr *fault.Error
	if vial.StatusCode(httpErr) != http.StatusInternalServerError || vial.StatusCode(appErr) != http.StatusInternalServerError {
		t.Fatal("typed nil errors did not map to 500")
	}
}
