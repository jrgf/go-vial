package main

import (
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jrgf/go-vial"
)

var testKey = []byte("0123456789abcdef0123456789abcdef")

func TestSessionAndFlashRoundTrip(t *testing.T) {
	app := testApp(t, false, testKey)
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

func TestCookiePolicy(t *testing.T) {
	app := testApp(t, true, testKey)
	response := httptest.NewRecorder()
	app.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/login?user=Rafa", nil))
	result := response.Result()
	t.Cleanup(func() {
		if err := result.Body.Close(); err != nil {
			t.Errorf("close response: %v", err)
		}
	})
	cookies := result.Cookies()
	if len(cookies) != 1 {
		t.Fatalf("cookies=%d", len(cookies))
	}
	cookie := cookies[0]
	if !cookie.Secure || !cookie.HttpOnly || cookie.SameSite != http.SameSiteLaxMode || cookie.Path != "/" {
		t.Fatalf("cookie=%#v", cookie)
	}
}

func TestKeyRotation(t *testing.T) {
	oldKey := []byte("old-key-0123456789abcdef0123456789")
	newKey := []byte("new-key-0123456789abcdef0123456789")
	oldApp := testApp(t, false, oldKey)
	oldCookie := loginCookie(t, oldApp)

	rotatedApp := testApp(t, false, newKey, oldKey)
	request := httptest.NewRequest(http.MethodGet, "/session", nil)
	request.AddCookie(oldCookie)
	response := httptest.NewRecorder()
	rotatedApp.ServeHTTP(response, request)
	result := response.Result()
	cookies := result.Cookies()
	if len(cookies) != 1 {
		t.Fatalf("rotated cookies=%d", len(cookies))
	}
	current := decodeSession(t, result)
	if current.User != "Rafa" {
		t.Fatalf("rotated session=%#v", current)
	}
	rotatedCookie := cookies[0]

	oldOnlyApp := testApp(t, false, oldKey)
	request = httptest.NewRequest(http.MethodGet, "/session", nil)
	request.AddCookie(rotatedCookie)
	response = httptest.NewRecorder()
	oldOnlyApp.ServeHTTP(response, request)
	rejected := decodeSession(t, response.Result())
	if rejected.User != "" {
		t.Fatalf("new-key cookie decoded with old key: %#v", rejected)
	}
}

func TestSessionConfiguration(t *testing.T) {
	if _, err := newSessionManager(false); err == nil {
		t.Fatal("expected missing key error")
	}
	if _, err := newSessionManager(false, []byte("short")); err == nil {
		t.Fatal("expected short key error")
	}
}

func testApp(t *testing.T, secure bool, keys ...[]byte) *vial.App {
	t.Helper()
	app, err := newApp(secure, keys...)
	if err != nil {
		t.Fatal(err)
	}
	return app
}

func loginCookie(t *testing.T, app *vial.App) *http.Cookie {
	t.Helper()
	response := httptest.NewRecorder()
	app.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/login?user=Rafa", nil))
	result := response.Result()
	if err := result.Body.Close(); err != nil {
		t.Fatal(err)
	}
	cookies := result.Cookies()
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
	if err := response.Body.Close(); err != nil {
		t.Fatal(err)
	}
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
	defer func() {
		if err := response.Body.Close(); err != nil {
			t.Errorf("close response: %v", err)
		}
	}()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", response.StatusCode)
	}
	var payload sessionResponse
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	return payload
}

func TestTamperedCookieIsRejected(t *testing.T) {
	app := testApp(t, false, testKey)
	request := httptest.NewRequest(http.MethodGet, "/session", nil)
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: strings.Repeat("x", 64)})
	response := httptest.NewRecorder()
	app.ServeHTTP(response, request)
	result := response.Result()
	cookies := result.Cookies()
	payload := decodeSession(t, result)
	if payload.User != "" || len(payload.Flashes) != 0 {
		t.Fatalf("session=%#v", payload)
	}
	if len(cookies) != 1 || cookies[0].MaxAge >= 0 {
		t.Fatalf("rejected cookie was not expired: %#v", cookies)
	}
}

func TestOversizedSessionDoesNotSetCookie(t *testing.T) {
	app := testApp(t, false, testKey)
	response := httptest.NewRecorder()
	target := "/login?user=" + strings.Repeat("x", maxCookieBytes)
	app.ServeHTTP(response, httptest.NewRequest(http.MethodPost, target, nil))
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d", response.Code)
	}
	if got := response.Header().Values("Set-Cookie"); len(got) != 0 {
		t.Fatalf("cookies=%#v", got)
	}
}
