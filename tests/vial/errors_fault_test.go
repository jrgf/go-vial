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
	httpErr.Headers = http.Header{"x-transport": []string{"http"}}

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

func TestProblemDetailsErrorHandler(t *testing.T) {
	validation := fault.Wrap(fault.InvalidArgument, "invalid_user", "User is invalid", errors.New("secret cause"))
	validation.Fields = map[string]string{"email": "required"}
	validation.Meta = map[string]any{"secret": "internal metadata"}
	internal := fault.Wrap(fault.Internal, "database_failed", "secret database message", errors.New("secret cause"))
	internal.Fields = map[string]string{"secret": "internal field"}
	challenged := vial.Unauthorized("login_required", "Please sign in")
	challenged.Headers = http.Header{"WWW-Authenticate": {"Bearer"}}
	explicit := vial.WrapHTTPError(http.StatusInternalServerError, "upstream_failed", "Upstream unavailable", errors.New("secret cause"))
	var typedNil *vial.HTTPError
	tests := []struct {
		name, method, path, code, detail string
		err                              error
		status                           int
		header, value                    string
	}{
		{name: "validation", err: fmt.Errorf("secret wrapper: %w", validation), status: 400, code: "invalid_user", detail: "User is invalid"},
		{name: "internal fault", err: internal, status: 500, code: "database_failed", detail: "An unexpected error occurred"},
		{name: "unknown", err: errors.New("secret failure"), status: 500, code: "internal_server_error", detail: "An unexpected error occurred"},
		{name: "typed nil", err: typedNil, status: 500, code: "internal_server_error", detail: "An unexpected error occurred"},
		{name: "explicit public error", err: explicit, status: 500, code: "upstream_failed", detail: "Upstream unavailable"},
		{name: "challenge", err: challenged, status: 401, code: "login_required", detail: "Please sign in", header: "WWW-Authenticate", value: "Bearer"},
		{name: "retry", err: vial.ErrAsyncQueueFull, status: 503, code: "async_queue_full", detail: "The operation could not be accepted at this time", header: "Retry-After", value: "5"},
		{name: "custom status", err: vial.NewHTTPError(499, "client_closed", "Request closed"), status: 499, code: "client_closed", detail: "Request closed"},
		{name: "not found", path: "/missing", status: 404},
		{name: "method", method: http.MethodPost, status: 405, header: "Allow", value: "GET, HEAD"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			app := vial.New()
			app.SetErrorHandler(vial.ProblemDetailsErrorHandler)
			app.Get("/resource", func(*vial.Context) error { return test.err })
			method, path := test.method, test.path
			if method == "" {
				method = http.MethodGet
			}
			if path == "" {
				path = "/resource"
			}
			response := httptest.NewRecorder()
			app.ServeHTTP(response, httptest.NewRequest(method, path+"?token=secret", nil))
			var problem struct {
				Type, Title, Detail, Code string
				Status                    int
				Fields                    map[string]string
			}
			if err := json.Unmarshal(response.Body.Bytes(), &problem); err != nil {
				t.Fatal(err)
			}
			if response.Code != test.status || problem.Status != test.status || problem.Type != "about:blank" || problem.Title != http.StatusText(test.status) || response.Header().Get("Content-Type") != "application/problem+json" {
				t.Fatalf("unexpected Problem Details response: status=%d headers=%v body=%s", response.Code, response.Header(), response.Body.String())
			}
			if test.code != "" && (problem.Code != test.code || problem.Detail != test.detail) {
				t.Fatalf("public error mapping changed: %+v", problem)
			}
			if test.header != "" && response.Header().Get(test.header) != test.value {
				t.Fatalf("lost %s header: %v", test.header, response.Header())
			}
			if test.name == "validation" && problem.Fields["email"] != "required" {
				t.Fatalf("lost validation fields: %+v", problem)
			}
			if strings.Contains(response.Body.String(), "secret") || strings.Contains(response.Body.String(), `"error":`) || strings.Contains(response.Body.String(), `"message":`) {
				t.Fatalf("unexpected private data or legacy envelope: %s", response.Body.String())
			}
		})
	}
}

func TestProblemDetailsPreservesCommittedResponse(t *testing.T) {
	app := vial.New()
	app.SetErrorHandler(vial.ProblemDetailsErrorHandler)
	app.Get("/", func(c *vial.Context) error {
		if err := c.Text(http.StatusAccepted, "already written"); err != nil {
			return err
		}
		return errors.New("secret late failure")
	})
	response := httptest.NewRecorder()
	app.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
	if response.Code != http.StatusAccepted || response.Body.String() != "already written" || response.Result().Header.Get("Content-Type") != "text/plain; charset=utf-8" {
		t.Fatalf("committed response changed: %d %s", response.Code, response.Body.String())
	}
}
