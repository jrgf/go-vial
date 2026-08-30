package main

import (
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jrgf/go-vial"
	"github.com/jrgf/go-vial/session"
)

var testKey = []byte("0123456789abcdef0123456789abcdef")

func FuzzSessionCookieDecode(fuzz *testing.F) {
	app, sessions := testApp(fuzz, false, testKey)
	fuzz.Add("")
	fuzz.Add("malformed-cookie")
	fuzz.Fuzz(func(t *testing.T, value string) {
		if len(value) > session.MaxCookieBytes {
			t.Skip()
		}
		request := httptest.NewRequest(http.MethodGet, "/session", nil)
		request.AddCookie(&http.Cookie{Name: sessions.CookieName(), Value: value})
		app.ServeHTTP(httptest.NewRecorder(), request)
	})
}

func TestSessionAndFlashRoundTrip(t *testing.T) {
	app, _ := testApp(t, false, testKey)
	server := httptest.NewServer(app)
	t.Cleanup(server.Close)
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := server.Client()
	client.Jar = jar

	requireStatus(t, do(t, client, http.MethodPost, server.URL+"/login?user=Rafa"), http.StatusNoContent)
	first := decodeSession(t, do(t, client, http.MethodGet, server.URL+"/session"))
	if first.User != "Rafa" || len(first.Flashes) != 1 || first.Flashes[0] != "Welcome Rafa" {
		t.Fatalf("first session=%#v", first)
	}
	second := decodeSession(t, do(t, client, http.MethodGet, server.URL+"/session"))
	if second.User != "Rafa" || len(second.Flashes) != 0 {
		t.Fatalf("second session=%#v", second)
	}
	requireStatus(t, do(t, client, http.MethodDelete, server.URL+"/session"), http.StatusNoContent)
	cleared := decodeSession(t, do(t, client, http.MethodGet, server.URL+"/session"))
	if cleared.User != "" || len(cleared.Flashes) != 0 {
		t.Fatalf("cleared session=%#v", cleared)
	}
}

func TestCookiePolicyAndTamperRejection(t *testing.T) {
	secureApp, _ := testApp(t, true, testKey)
	response := httptest.NewRecorder()
	secureApp.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/login?user=Rafa", nil))
	cookies := response.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("cookies=%d", len(cookies))
	}
	cookie := cookies[0]
	if cookie.Name != "__Host-vial_session" || !cookie.Secure || !cookie.HttpOnly || cookie.SameSite != http.SameSiteLaxMode || cookie.Path != "/" {
		t.Fatalf("cookie=%#v", cookie)
	}

	app, sessions := testApp(t, false, testKey)
	request := httptest.NewRequest(http.MethodGet, "/session", nil)
	request.AddCookie(&http.Cookie{Name: sessions.CookieName(), Value: strings.Repeat("x", 64)})
	response = httptest.NewRecorder()
	app.ServeHTTP(response, request)
	result := response.Result()
	payload := decodeSession(t, result)
	if payload.User != "" || len(payload.Flashes) != 0 {
		t.Fatalf("session=%#v", payload)
	}
	if cookies := result.Cookies(); len(cookies) != 1 || cookies[0].MaxAge >= 0 {
		t.Fatalf("rejected cookie was not expired: %#v", cookies)
	}
}

func TestKeyRotationAndReload(t *testing.T) {
	oldKey := []byte("old-key-0123456789abcdef0123456789")
	newKey := []byte("new-key-0123456789abcdef0123456789")
	oldApp, _ := testApp(t, false, oldKey)
	oldCookie := loginCookie(t, oldApp)

	rotatedApp, _ := testApp(t, false, newKey, oldKey)
	request := httptest.NewRequest(http.MethodGet, "/session", nil)
	request.AddCookie(oldCookie)
	response := httptest.NewRecorder()
	rotatedApp.ServeHTTP(response, request)
	result := response.Result()
	current := decodeSession(t, result)
	if current.User != "Rafa" {
		t.Fatalf("rotated session=%#v", current)
	}
	rotatedCookies := result.Cookies()
	if len(rotatedCookies) != 1 || rotatedCookies[0].Value == oldCookie.Value {
		t.Fatalf("rotated cookies=%#v", rotatedCookies)
	}

	reloadApp, manager := testApp(t, false, oldKey)
	reloadCookie := loginCookie(t, reloadApp)
	keyFile := filepath.Join(t.TempDir(), "session-keys")
	if err := os.WriteFile(keyFile, append(append(newKey, ','), oldKey...), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := reloadSessionKeys(manager, keyFile); err != nil {
		t.Fatal(err)
	}
	request = httptest.NewRequest(http.MethodGet, "/session", nil)
	request.AddCookie(reloadCookie)
	response = httptest.NewRecorder()
	reloadApp.ServeHTTP(response, request)
	result = response.Result()
	current = decodeSession(t, result)
	if current.User != "Rafa" || len(result.Cookies()) != 1 {
		t.Fatalf("reloaded session=%#v cookies=%#v", current, result.Cookies())
	}

	if err := os.WriteFile(keyFile, []byte("short"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := reloadSessionKeys(manager, keyFile); err == nil {
		t.Fatal("expected invalid key reload to fail")
	}
}

func TestSessionConfigurationAndSizeLimit(t *testing.T) {
	if _, _, err := newApp(false); err == nil {
		t.Fatal("expected missing key error")
	}
	if _, _, err := newApp(false, []byte("short")); err == nil {
		t.Fatal("expected short key error")
	}
	app, _ := testApp(t, false, testKey)
	response := httptest.NewRecorder()
	target := "/login?user=" + strings.Repeat("x", session.MaxCookieBytes)
	app.ServeHTTP(response, httptest.NewRequest(http.MethodPost, target, nil))
	if response.Code != http.StatusInternalServerError || len(response.Header().Values("Set-Cookie")) != 0 {
		t.Fatalf("status=%d cookies=%#v", response.Code, response.Header().Values("Set-Cookie"))
	}
}

func testApp(t testing.TB, secure bool, keys ...[]byte) (*vial.App, *session.Manager) {
	t.Helper()
	app, manager, err := newApp(secure, keys...)
	if err != nil {
		t.Fatal(err)
	}
	return app, manager
}

func loginCookie(t *testing.T, app *vial.App) *http.Cookie {
	t.Helper()
	response := httptest.NewRecorder()
	app.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/login?user=Rafa", nil))
	cookies := response.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("cookies=%d", len(cookies))
	}
	return cookies[0]
}

func do(t *testing.T, client *http.Client, method, target string) *http.Response {
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

type sessionResponse struct {
	User    string   `json:"user"`
	Flashes []string `json:"flashes"`
}

func decodeSession(t *testing.T, response *http.Response) sessionResponse {
	t.Helper()
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", response.StatusCode)
	}
	var payload sessionResponse
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	return payload
}
