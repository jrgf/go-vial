package session

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jrgf/go-vial"
	"github.com/jrgf/go-vial/middleware"
)

func TestMutationsPersistOnEmptyAndErrorResponses(t *testing.T) {
	for _, outcome := range []string{"empty", "error", "panic"} {
		t.Run(outcome, func(t *testing.T) {
			manager, err := New(Config{Keys: [][]byte{[]byte("0123456789abcdef0123456789abcdef")}})
			if err != nil {
				t.Fatal(err)
			}
			app := vial.New()
			app.Use(middleware.Recover(), manager.Middleware())
			app.Get("/", func(c *vial.Context) error {
				current, err := manager.From(c)
				if err != nil {
					return err
				}
				if outcome == "empty" {
					return current.Set("theme", "dark")
				}
				if err := current.Destroy(); err != nil {
					return err
				}
				if outcome == "panic" {
					panic("logout failed")
				}
				return vial.Unauthorized("logout", "logged out")
			})
			r := httptest.NewRecorder()
			app.ServeHTTP(r, httptest.NewRequest("GET", "/", nil))
			cookies := r.Result().Cookies()
			if len(cookies) != 1 {
				t.Fatalf("cookies=%v", cookies)
			}
			if outcome != "empty" && cookies[0].MaxAge != -1 {
				t.Fatalf("session not expired: %v", cookies[0])
			}
		})
	}
}

func TestAbsoluteLifetimeSurvivesWritesAndRotation(t *testing.T) {
	manager, err := New(Config{Keys: [][]byte{[]byte("0123456789abcdef0123456789abcdef")}, MaxAge: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Unix(2_000_000_000, 0)
	now := start
	manager.now = func() time.Time { return now }
	app := vial.New()
	app.Use(manager.Middleware())
	app.Get("/", func(c *vial.Context) error {
		s, err := manager.From(c)
		if err != nil {
			return err
		}
		if c.Query("read") == "" {
			if err := s.Set("value", now.String()); err != nil {
				return err
			}
		}
		return c.NoContent(204)
	})
	request := func(cookie *http.Cookie, path string) *http.Cookie {
		t.Helper()
		r := httptest.NewRequest("GET", path, nil)
		if cookie != nil {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		app.ServeHTTP(w, r)
		cookies := w.Result().Cookies()
		if len(cookies) != 1 {
			t.Fatalf("cookies=%v", cookies)
		}
		return cookies[0]
	}
	cookie := request(nil, "/")
	now = start.Add(20 * time.Second)
	cookie = request(cookie, "/")
	if cookie.MaxAge != 40 || !cookie.Expires.Equal(start.Add(time.Minute)) {
		t.Errorf("mutation extended expiry: %v", cookie)
	}
	if err := manager.ReplaceKeys([]byte("abcdef0123456789abcdef0123456789ab"), []byte("0123456789abcdef0123456789abcdef")); err != nil {
		t.Fatal(err)
	}
	now = start.Add(40 * time.Second)
	cookie = request(cookie, "/?read=1")
	if cookie.MaxAge != 20 || !cookie.Expires.Equal(start.Add(time.Minute)) {
		t.Errorf("rotation extended expiry: %v", cookie)
	}
	now = start.Add(time.Minute)
	if _, _, ok := manager.decode(cookie.Value); ok {
		t.Fatal("session accepted at absolute expiry")
	}
}
