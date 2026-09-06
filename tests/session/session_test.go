package session_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jrgf/go-vial"
	"github.com/jrgf/go-vial/session"
)

var (
	oldKey = []byte("old-key-0123456789abcdef0123456789")
	newKey = []byte("new-key-0123456789abcdef0123456789")
)

func TestSessionAndFlashRoundTrip(t *testing.T) {
	manager := newTestManager(t, oldKey)
	server := httptest.NewServer(sessionApp(manager))
	t.Cleanup(server.Close)
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := server.Client()
	client.Jar = jar

	requireStatus(t, request(t, client, http.MethodPost, server.URL+"/login"), http.StatusNoContent)
	first := readState(t, request(t, client, http.MethodGet, server.URL+"/session"))
	if first.User != "User" || len(first.Flashes) != 1 || first.Flashes[0] != "Welcome User" {
		t.Fatalf("first session=%#v", first)
	}
	second := readState(t, request(t, client, http.MethodGet, server.URL+"/session"))
	if second.User != "User" || len(second.Flashes) != 0 {
		t.Fatalf("second session=%#v", second)
	}
	requireStatus(t, request(t, client, http.MethodDelete, server.URL+"/session"), http.StatusNoContent)
	cleared := readState(t, request(t, client, http.MethodGet, server.URL+"/session"))
	if cleared.User != "" || len(cleared.Flashes) != 0 {
		t.Fatalf("cleared session=%#v", cleared)
	}
}

func TestSecureCookieDefaults(t *testing.T) {
	manager, err := session.New(session.Config{Keys: [][]byte{oldKey}})
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	sessionApp(manager).ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/login", nil))
	cookies := response.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("cookies=%d", len(cookies))
	}
	cookie := cookies[0]
	if cookie.Name != "__Host-vial_session" || !cookie.Secure || !cookie.HttpOnly || cookie.Path != "/" || cookie.Domain != "" || cookie.SameSite != http.SameSiteLaxMode {
		t.Fatalf("cookie=%#v", cookie)
	}
	if cookie.MaxAge <= 0 || !cookie.Expires.After(time.Now()) {
		t.Fatalf("cookie lifetime=%#v", cookie)
	}
}

func TestTamperRejectionAndKeyRotation(t *testing.T) {
	oldManager := newTestManager(t, oldKey)
	oldCookie := setCookie(t, oldManager)

	rotated := newTestManager(t, newKey, oldKey)
	response := peek(t, rotated, oldCookie)
	state := readState(t, response)
	if state.User != "User" {
		t.Fatalf("rotated session=%#v", state)
	}
	rotatedCookies := response.Cookies()
	if len(rotatedCookies) != 1 || rotatedCookies[0].Value == oldCookie.Value {
		t.Fatalf("cookie was not rotated: %#v", rotatedCookies)
	}

	oldOnly := newTestManager(t, oldKey)
	rejected := peek(t, oldOnly, rotatedCookies[0])
	state = readState(t, rejected)
	if state.User != "" {
		t.Fatalf("new-key cookie decoded with old key: %#v", state)
	}
	if cookies := rejected.Cookies(); len(cookies) != 1 || cookies[0].MaxAge >= 0 {
		t.Fatalf("rejected cookie was not expired: %#v", cookies)
	}

	tampered := &http.Cookie{Name: oldManager.CookieName(), Value: strings.Repeat("x", 64)}
	rejected = peek(t, oldManager, tampered)
	_ = readState(t, rejected)
	if cookies := rejected.Cookies(); len(cookies) != 1 || cookies[0].MaxAge >= 0 {
		t.Fatalf("tampered cookie was not expired: %#v", cookies)
	}
}

func TestMutationLimitsAndCommitBoundary(t *testing.T) {
	manager := newTestManager(t, oldKey)
	app := vial.New()
	app.Use(manager.Middleware())
	var oversizedErr, committedErr error
	app.Get("/oversized", func(context *vial.Context) error {
		current, err := manager.From(context)
		if err != nil {
			return err
		}
		if err := current.Set("partial", "must not persist"); err != nil {
			return err
		}
		oversizedErr = current.Set("large", strings.Repeat("x", session.MaxCookieBytes))
		return oversizedErr
	})
	app.Get("/committed", func(context *vial.Context) error {
		current, err := manager.From(context)
		if err != nil {
			return err
		}
		if err := context.NoContent(http.StatusNoContent); err != nil {
			return err
		}
		committedErr = current.Set("late", "value")
		return nil
	})

	response := httptest.NewRecorder()
	app.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/oversized", nil))
	if response.Code != http.StatusInternalServerError || !errors.Is(oversizedErr, session.ErrTooLarge) || len(response.Header().Values("Set-Cookie")) != 0 {
		t.Fatalf("oversized: status=%d error=%v cookies=%#v", response.Code, oversizedErr, response.Header().Values("Set-Cookie"))
	}
	response = httptest.NewRecorder()
	app.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/committed", nil))
	if response.Code != http.StatusNoContent || !errors.Is(committedErr, session.ErrCommitted) || len(response.Header().Values("Set-Cookie")) != 0 {
		t.Fatalf("committed: status=%d error=%v cookies=%#v", response.Code, committedErr, response.Header().Values("Set-Cookie"))
	}
}

func TestConfigurationAndAtomicKeyReplacement(t *testing.T) {
	tests := []session.Config{
		{},
		{Keys: [][]byte{[]byte("short")}},
		{Keys: [][]byte{oldKey}, Path: "relative"},
		{Keys: [][]byte{oldKey}, Name: "__Host-test", DangerouslyAllowInsecureCookies: true},
		{Keys: [][]byte{oldKey}, SameSite: http.SameSiteNoneMode, DangerouslyAllowInsecureCookies: true},
	}
	for _, config := range tests {
		if _, err := session.New(config); err == nil {
			t.Fatalf("expected configuration error for %#v", config)
		}
	}

	manager := newTestManager(t, oldKey)
	cookie := setCookie(t, manager)
	if err := manager.ReplaceKeys([]byte("short")); err == nil {
		t.Fatal("expected key replacement error")
	}
	state := readState(t, peek(t, manager, cookie))
	if state.User != "User" {
		t.Fatalf("failed replacement discarded active key: %#v", state)
	}

	app := vial.New()
	var unavailable error
	app.Get("/", func(context *vial.Context) error {
		_, unavailable = manager.From(context)
		return context.NoContent(http.StatusNoContent)
	})
	app.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	if !errors.Is(unavailable, session.ErrUnavailable) {
		t.Fatalf("unavailable error=%v", unavailable)
	}
}

type state struct {
	User    string   `json:"user"`
	Flashes []string `json:"flashes"`
}

func newTestManager(t *testing.T, keys ...[]byte) *session.Manager {
	t.Helper()
	manager, err := session.New(session.Config{
		Keys:                            keys,
		MaxAge:                          time.Hour,
		DangerouslyAllowInsecureCookies: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	return manager
}

func sessionApp(manager *session.Manager) *vial.App {
	app := vial.New()
	app.Use(manager.Middleware())
	app.Post("/login", func(context *vial.Context) error {
		current, err := manager.From(context)
		if err != nil {
			return err
		}
		if err := current.Set("user", "User"); err != nil {
			return err
		}
		if err := current.AddFlash("Welcome User"); err != nil {
			return err
		}
		return context.NoContent(http.StatusNoContent)
	})
	app.Get("/session", func(context *vial.Context) error {
		current, err := manager.From(context)
		if err != nil {
			return err
		}
		flashes, err := current.Flashes()
		if err != nil {
			return err
		}
		user, _ := current.Get("user")
		return context.JSON(http.StatusOK, state{User: user, Flashes: flashes})
	})
	app.Delete("/session", func(context *vial.Context) error {
		current, err := manager.From(context)
		if err != nil {
			return err
		}
		if err := current.Destroy(); err != nil {
			return err
		}
		return context.NoContent(http.StatusNoContent)
	})
	return app
}

func valueApp(manager *session.Manager) *vial.App {
	app := vial.New()
	app.Use(manager.Middleware())
	app.Post("/set", func(context *vial.Context) error {
		current, err := manager.From(context)
		if err != nil {
			return err
		}
		if err := current.Set("user", "User"); err != nil {
			return err
		}
		return context.NoContent(http.StatusNoContent)
	})
	app.Get("/peek", func(context *vial.Context) error {
		current, err := manager.From(context)
		if err != nil {
			return err
		}
		user, _ := current.Get("user")
		return context.JSON(http.StatusOK, state{User: user, Flashes: []string{}})
	})
	return app
}

func setCookie(t *testing.T, manager *session.Manager) *http.Cookie {
	t.Helper()
	response := httptest.NewRecorder()
	valueApp(manager).ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/set", nil))
	cookies := response.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("cookies=%d", len(cookies))
	}
	return cookies[0]
}

func peek(t *testing.T, manager *session.Manager, cookie *http.Cookie) *http.Response {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "/peek", nil)
	request.AddCookie(cookie)
	response := httptest.NewRecorder()
	valueApp(manager).ServeHTTP(response, request)
	return response.Result()
}

func request(t *testing.T, client *http.Client, method, target string) *http.Response {
	t.Helper()
	request, err := http.NewRequest(method, target, nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func requireStatus(t *testing.T, response *http.Response, want int) {
	t.Helper()
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != want {
		t.Fatalf("status=%d want=%d", response.StatusCode, want)
	}
}

func readState(t *testing.T, response *http.Response) state {
	t.Helper()
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", response.StatusCode)
	}
	var value state
	if err := json.NewDecoder(response.Body).Decode(&value); err != nil {
		t.Fatal(err)
	}
	return value
}
