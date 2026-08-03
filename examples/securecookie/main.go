package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/securecookie"
	"github.com/jrgf/go-vial"
)

const (
	sessionCookieName = "vial_session"
	sessionMaxAge     = 5 * time.Minute
	keyRefreshEvery   = time.Minute
	maxCookieBytes    = 4096
)

type sessionData struct {
	Values  map[string]string `json:"values,omitempty"`
	Flashes []string          `json:"flashes,omitempty"`
}

var sessionContextKey = vial.NewValueKey[*sessionData]("securecookie_session")

type sessionManager struct {
	mu     sync.RWMutex
	codecs []securecookie.Codec
	secure bool
}

func main() {
	keyFile := strings.TrimSpace(os.Getenv("SESSION_KEYS_FILE"))
	keys := parseSessionKeys(os.Getenv("SESSION_KEYS"))
	if keyFile != "" {
		var err error
		keys, err = readSessionKeys(keyFile)
		if err != nil {
			slog.Error("read session keys", "error", err)
			os.Exit(1)
		}
	}

	app, sessions, err := newApp(os.Getenv("VIAL_ALLOW_INSECURE_COOKIE") != "1", keys...)
	if err != nil {
		slog.Error("build application", "error", err)
		os.Exit(1)
	}
	if keyFile != "" {
		app.Go("session-key-refresh", sessions.refreshKeys(keyFile), vial.NonCritical())
	}
	address := os.Getenv("ADDR")
	if address == "" {
		address = ":8080"
	}
	if err := app.Run(context.Background(), address); err != nil {
		slog.Error("application stopped with an error", "error", err)
		os.Exit(1)
	}
}

func newApp(secure bool, keys ...[]byte) (*vial.App, *sessionManager, error) {
	sessions, err := newSessionManager(secure, keys...)
	if err != nil {
		return nil, nil, err
	}

	app := vial.New()
	app.Use(sessions.middleware())
	app.Post("/login", func(context *vial.Context) error {
		user := strings.TrimSpace(context.Query("user"))
		if user == "" {
			return vial.BadRequest("user_required", "Query parameter user is required")
		}
		current, err := sessions.from(context)
		if err != nil {
			return err
		}
		current.Values["user"] = user
		current.Flashes = append(current.Flashes, "Welcome "+user)
		if err := sessions.save(context, current); err != nil {
			return err
		}
		return context.NoContent(http.StatusNoContent)
	})
	app.Get("/session", func(context *vial.Context) error {
		current, err := sessions.from(context)
		if err != nil {
			return err
		}
		flashes := append([]string(nil), current.Flashes...)
		if len(current.Flashes) > 0 {
			current.Flashes = nil
			if err := sessions.save(context, current); err != nil {
				return err
			}
		}
		return context.JSON(http.StatusOK, struct {
			User    string   `json:"user,omitempty"`
			Flashes []string `json:"flashes"`
		}{User: current.Values["user"], Flashes: flashes})
	})
	app.Delete("/session", func(context *vial.Context) error {
		if err := sessions.destroy(context); err != nil {
			return err
		}
		return context.NoContent(http.StatusNoContent)
	})
	return app, sessions, nil
}

func newSessionManager(secure bool, keys ...[]byte) (*sessionManager, error) {
	manager := &sessionManager{secure: secure}
	if err := manager.replaceKeys(keys...); err != nil {
		return nil, err
	}
	return manager, nil
}

func (manager *sessionManager) replaceKeys(keys ...[]byte) error {
	if len(keys) == 0 {
		return errors.New("securecookie example: at least one session key is required")
	}
	codecs := make([]securecookie.Codec, 0, len(keys))
	for index, key := range keys {
		if len(key) < 32 {
			return fmt.Errorf("securecookie example: session key %d must be at least 32 bytes", index)
		}
		copied := append([]byte(nil), key...)
		codec := securecookie.New(copied, nil)
		codec.MaxAge(int(sessionMaxAge / time.Second))
		codec.MaxLength(maxCookieBytes)
		codec.SetSerializer(securecookie.JSONEncoder{})
		codecs = append(codecs, codec)
	}
	manager.mu.Lock()
	manager.codecs = codecs
	manager.mu.Unlock()
	return nil
}

func (manager *sessionManager) currentCodecs() []securecookie.Codec {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	return manager.codecs
}

func (manager *sessionManager) refreshKeys(path string) vial.Task {
	return func(context context.Context) error {
		ticker := time.NewTicker(keyRefreshEvery)
		defer ticker.Stop()
		for {
			select {
			case <-context.Done():
				return context.Err()
			case <-ticker.C:
				if err := manager.reloadKeys(path); err != nil {
					slog.Warn("keep current session keys", "error", err)
					continue
				}
				slog.Info("session keys refreshed")
			}
		}
	}
}

func (manager *sessionManager) reloadKeys(path string) error {
	keys, err := readSessionKeys(path)
	if err != nil {
		return err
	}
	return manager.replaceKeys(keys...)
}

func readSessionKeys(path string) ([][]byte, error) {
	value, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return parseSessionKeys(string(value)), nil
}

func parseSessionKeys(value string) [][]byte {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	keys := make([][]byte, 0, len(parts))
	for _, key := range parts {
		keys = append(keys, []byte(strings.TrimSpace(key)))
	}
	return keys
}

func (manager *sessionManager) middleware() vial.Middleware {
	return func(next vial.Handler) vial.Handler {
		return func(context *vial.Context) error {
			current := newSessionData()
			cookie, err := context.Request().Cookie(sessionCookieName)
			switch {
			case errors.Is(err, http.ErrNoCookie):
			case err != nil:
				return fmt.Errorf("read session cookie: %w", err)
			default:
				if err := securecookie.DecodeMulti(sessionCookieName, cookie.Value, current, manager.currentCodecs()...); err != nil {
					var cookieErr securecookie.Error
					if !errors.As(err, &cookieErr) || !cookieErr.IsDecode() {
						return fmt.Errorf("decode session cookie: %w", err)
					}
					current = newSessionData()
					if err := manager.expire(context); err != nil {
						return err
					}
				}
			}
			if current.Values == nil {
				current.Values = make(map[string]string)
			}
			sessionContextKey.Set(context, current)
			return next(context)
		}
	}
}

func (manager *sessionManager) from(context *vial.Context) (*sessionData, error) {
	current, ok := sessionContextKey.Get(context)
	if !ok {
		return nil, errors.New("securecookie example: session middleware is not installed")
	}
	return current, nil
}

func (manager *sessionManager) save(context *vial.Context, current *sessionData) error {
	if context.Committed() {
		return errors.New("securecookie example: response already committed")
	}
	if len(current.Values) == 0 && len(current.Flashes) == 0 {
		return manager.destroy(context)
	}
	value, err := securecookie.EncodeMulti(sessionCookieName, current, manager.currentCodecs()...)
	if err != nil {
		return fmt.Errorf("encode session cookie: %w", err)
	}
	return manager.writeCookie(context, value, int(sessionMaxAge/time.Second), time.Now().Add(sessionMaxAge))
}

func (manager *sessionManager) destroy(context *vial.Context) error {
	if context.Committed() {
		return errors.New("securecookie example: response already committed")
	}
	current, err := manager.from(context)
	if err != nil {
		return err
	}
	current.Values = make(map[string]string)
	current.Flashes = nil
	return manager.expire(context)
}

func (manager *sessionManager) expire(context *vial.Context) error {
	return manager.writeCookie(context, "", -1, time.Unix(1, 0))
}

func (manager *sessionManager) writeCookie(context *vial.Context, value string, maxAge int, expires time.Time) error {
	cookie := (&http.Cookie{
		Name:     sessionCookieName,
		Value:    value,
		Path:     "/",
		Expires:  expires,
		MaxAge:   maxAge,
		Secure:   manager.secure,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	}).String()
	if cookie == "" {
		return errors.New("securecookie example: invalid session cookie")
	}
	if len(cookie) > maxCookieBytes {
		return fmt.Errorf("securecookie example: session cookie exceeds %d bytes", maxCookieBytes)
	}
	context.Response().Header().Add("Set-Cookie", cookie)
	return nil
}

func newSessionData() *sessionData {
	return &sessionData{Values: make(map[string]string)}
}
