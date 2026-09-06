package auth_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jrgf/go-vial"
	"github.com/jrgf/go-vial/auth"
)

func TestAuthenticationAndGrantGuards(t *testing.T) {
	var calls int
	manager := newManager(t, func(context *vial.Context) (auth.Identity, bool, error) {
		calls++
		switch context.Request().Header.Get("X-Credential") {
		case "":
			return auth.Identity{}, false, nil
		case "invalid":
			return auth.Identity{}, false, auth.ErrInvalidCredentials
		case "failure":
			return auth.Identity{}, false, errors.New("private identity provider failure")
		case "bad-subject":
			return auth.Identity{Subject: " "}, true, nil
		case "bad-grant":
			return auth.Identity{Subject: "user", Grants: []string{" "}}, true, nil
		case "contradictory":
			return auth.Identity{Subject: "user"}, false, nil
		case "admin":
			return auth.Identity{Subject: "admin", Grants: []string{"read", "admin"}}, true, nil
		default:
			return auth.Identity{Subject: "user", Grants: []string{"read", "read"}}, true, nil
		}
	})

	app := vial.New()
	app.Use(manager.Middleware())
	app.Get("/public", func(context *vial.Context) error {
		identity, authenticated := manager.From(context)
		return context.JSON(http.StatusOK, map[string]any{
			"authenticated": authenticated,
			"subject":       identity.Subject,
		})
	})
	app.Get("/private", func(context *vial.Context) error {
		identity, _ := manager.From(context)
		return context.JSON(http.StatusOK, identity)
	}, vial.RouteMiddleware(manager.Require()))
	app.Get("/read", func(context *vial.Context) error {
		return context.NoContent(http.StatusNoContent)
	}, vial.RouteMiddleware(manager.Require("read")))
	app.Get("/admin", func(context *vial.Context) error {
		return context.NoContent(http.StatusNoContent)
	}, vial.RouteMiddleware(manager.Require("read", "admin")))
	app.Get("/copy", func(context *vial.Context) error {
		identity, _ := manager.From(context)
		if len(identity.Grants) != 1 {
			return errors.New("duplicate grants were not normalized")
		}
		identity.Grants[0] = "admin"
		stored, _ := manager.From(context)
		if stored.Has("admin") {
			return errors.New("returned identity mutated request auth state")
		}
		return context.NoContent(http.StatusNoContent)
	}, vial.RouteMiddleware(manager.Require()))

	response := serve(app, "/public", "")
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"authenticated":false`) {
		t.Fatalf("anonymous public response: status=%d body=%s", response.Code, response.Body.String())
	}
	response = serve(app, "/private", "")
	requireAuthFailure(t, response, http.StatusUnauthorized, "authentication_required", true)
	response = serve(app, "/public", "invalid")
	requireAuthFailure(t, response, http.StatusUnauthorized, "invalid_credentials", true)

	before := calls
	response = serve(app, "/private", "user")
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"Subject":"user"`) || calls != before+1 {
		t.Fatalf("authenticated response: status=%d calls=%d body=%s", response.Code, calls, response.Body.String())
	}
	if response = serve(app, "/read", "user"); response.Code != http.StatusNoContent {
		t.Fatalf("read grant status=%d", response.Code)
	}
	response = serve(app, "/admin", "user")
	requireAuthFailure(t, response, http.StatusForbidden, "insufficient_grant", false)
	if response = serve(app, "/admin", "admin"); response.Code != http.StatusNoContent {
		t.Fatalf("admin grant status=%d", response.Code)
	}
	if response = serve(app, "/copy", "user"); response.Code != http.StatusNoContent {
		t.Fatalf("identity copy status=%d body=%s", response.Code, response.Body.String())
	}

	for _, credential := range []string{"failure", "bad-subject", "bad-grant", "contradictory"} {
		response = serve(app, "/public", credential)
		if response.Code != http.StatusInternalServerError || strings.Contains(response.Body.String(), "identity provider") {
			t.Fatalf("credential %q: status=%d body=%s", credential, response.Code, response.Body.String())
		}
	}
}

func TestConfigurationAndMissingMiddleware(t *testing.T) {
	authenticator := func(*vial.Context) (auth.Identity, bool, error) {
		return auth.Identity{}, false, nil
	}
	for _, config := range []auth.Config{
		{},
		{Authenticate: authenticator},
		{Authenticate: authenticator, Challenge: " Bearer"},
		{Authenticate: authenticator, Challenge: "Bearer\r\nX-Test: value"},
		{Authenticate: authenticator, Challenge: "Bearer\x00"},
		{Authenticate: authenticator, Challenge: "=invalid"},
	} {
		if _, err := auth.New(config); err == nil {
			t.Fatalf("expected configuration error for %#v", config)
		}
	}

	manager := newManager(t, authenticator)
	if identity, ok := manager.From(nil); ok || identity.Subject != "" {
		t.Fatalf("nil context identity=%#v authenticated=%v", identity, ok)
	}
	app := vial.New()
	called := false
	app.Get("/", func(context *vial.Context) error {
		called = true
		return context.NoContent(http.StatusNoContent)
	}, vial.RouteMiddleware(manager.Require()))
	response := serve(app, "/", "")
	if called || response.Code != http.StatusInternalServerError {
		t.Fatalf("missing middleware: called=%v status=%d", called, response.Code)
	}
}

func TestIdentityHas(t *testing.T) {
	identity := auth.Identity{Subject: "user", Grants: []string{"read"}}
	if !identity.Has("read") || identity.Has("admin") {
		t.Fatalf("unexpected grant lookup for %#v", identity)
	}
}

func newManager(t *testing.T, authenticate auth.Authenticator) *auth.Manager {
	t.Helper()
	manager, err := auth.New(auth.Config{
		Authenticate: authenticate,
		Challenge:    `Session realm="vial"`,
	})
	if err != nil {
		t.Fatal(err)
	}
	return manager
}

func serve(app *vial.App, path, credential string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, path, nil)
	if credential != "" {
		request.Header.Set("X-Credential", credential)
	}
	response := httptest.NewRecorder()
	app.ServeHTTP(response, request)
	return response
}

func requireAuthFailure(t *testing.T, response *httptest.ResponseRecorder, status int, code string, challenged bool) {
	t.Helper()
	if response.Code != status {
		t.Fatalf("status=%d want=%d body=%s", response.Code, status, response.Body.String())
	}
	var payload struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil || payload.Error.Code != code {
		t.Fatalf("error=%v code=%q body=%s", err, payload.Error.Code, response.Body.String())
	}
	challenge := response.Header().Get("WWW-Authenticate")
	if challenged && challenge != `Session realm="vial"` {
		t.Fatalf("challenge=%q", challenge)
	}
	if !challenged && challenge != "" {
		t.Fatalf("unexpected challenge=%q", challenge)
	}
}
