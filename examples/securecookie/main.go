package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gorilla/securecookie"
	"github.com/jrgf/go-vial"
)

const (
	sessionCookieName = "vial_session"
	sessionContextKey = "example.securecookie.session"
	sessionMaxAge     = 8 * time.Hour
	maxCookieBytes    = 4096
)

type sessionData struct {
	Values  map[string]string `json:"values,omitempty"`
	Flashes []string          `json:"flashes,omitempty"`
}

type sessionManager struct {
	codecs []securecookie.Codec
	secure bool
}

func main() {
	var keys [][]byte
	if value := strings.TrimSpace(os.Getenv("SESSION_KEYS")); value != "" {
		for _, key := range strings.Split(value, ",") {
			keys = append(keys, []byte(strings.TrimSpace(key)))
		}
	}

	app, err := newApp(os.Getenv("VIAL_ALLOW_INSECURE_COOKIE") != "1", keys...)
	if err != nil {
		slog.Error("build application", "error", err)
		os.Exit(1)
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

func newApp(secure bool, keys ...[]byte) (*vial.App, error) {
	sessions, err := newSessionManager(secure, keys...)
	if err != nil {
		return nil, err
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
	return app, nil
}

func newSessionManager(secure bool, keys ...[]byte) (*sessionManager, error) {
	if len(keys) == 0 {
		return nil, errors.New("securecookie example: at least one session key is required")
	}
	codecs := make([]securecookie.Codec, 0, len(keys))
	for index, key := range keys {
		if len(key) < 32 {
			return nil, fmt.Errorf("securecookie example: session key %d must be at least 32 bytes", index)
		}
		copied := append([]byte(nil), key...)
		codec := securecookie.New(copied, nil)
		codec.MaxAge(int(sessionMaxAge / time.Second))
		codec.MaxLength(maxCookieBytes)
		codec.SetSerializer(securecookie.JSONEncoder{})
		codecs = append(codecs, codec)
	}
	return &sessionManager{codecs: codecs, secure: secure}, nil
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
				if err := securecookie.DecodeMulti(sessionCookieName, cookie.Value, current, manager.codecs...); err != nil {
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
			context.Set(sessionContextKey, current)
			return next(context)
		}
	}
}

func (manager *sessionManager) from(context *vial.Context) (*sessionData, error) {
	value, ok := context.Get(sessionContextKey)
	if !ok {
		return nil, errors.New("securecookie example: session middleware is not installed")
	}
	current, ok := value.(*sessionData)
	if !ok {
		return nil, errors.New("securecookie example: invalid request session")
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
	value, err := securecookie.EncodeMulti(sessionCookieName, current, manager.codecs...)
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
