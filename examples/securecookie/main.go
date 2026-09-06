package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/jrgf/go-vial"
	"github.com/jrgf/go-vial/auth"
	"github.com/jrgf/go-vial/session"
)

const (
	sessionMaxAge   = 5 * time.Minute
	keyRefreshEvery = time.Minute
)

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
		app.Go("session-key-refresh", refreshSessionKeys(sessions, keyFile), vial.NonCritical())
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

func newApp(secure bool, keys ...[]byte) (*vial.App, *session.Manager, error) {
	sessions, err := session.New(session.Config{
		Keys:                            keys,
		MaxAge:                          sessionMaxAge,
		DangerouslyAllowInsecureCookies: !secure,
	})
	if err != nil {
		return nil, nil, err
	}
	identities, err := auth.New(auth.Config{
		Challenge: `Session realm="vial-example"`,
		Authenticate: func(context *vial.Context) (auth.Identity, bool, error) {
			current, err := sessions.From(context)
			if err != nil {
				return auth.Identity{}, false, err
			}
			user, ok := current.Get("user")
			if !ok {
				return auth.Identity{}, false, nil
			}
			identity := auth.Identity{Subject: user, Grants: []string{"profile:read"}}
			if user == "Admin" {
				identity.Grants = append(identity.Grants, "admin")
			}
			return identity, true, nil
		},
	})
	if err != nil {
		return nil, nil, err
	}

	app := vial.New()
	app.Use(sessions.Middleware(), identities.Middleware())
	app.Post("/login", func(context *vial.Context) error {
		user := strings.TrimSpace(context.Query("user"))
		if user == "" {
			return vial.BadRequest("user_required", "Query parameter user is required")
		}
		current, err := sessions.From(context)
		if err != nil {
			return err
		}
		if err := current.Set("user", user); err != nil {
			return err
		}
		if err := current.AddFlash("Welcome " + user); err != nil {
			return err
		}
		return context.NoContent(http.StatusNoContent)
	})
	app.Get("/session", func(context *vial.Context) error {
		current, err := sessions.From(context)
		if err != nil {
			return err
		}
		flashes, err := current.Flashes()
		if err != nil {
			return err
		}
		user, _ := current.Get("user")
		return context.JSON(http.StatusOK, struct {
			User    string   `json:"user,omitempty"`
			Flashes []string `json:"flashes"`
		}{User: user, Flashes: flashes})
	})
	app.Delete("/session", func(context *vial.Context) error {
		current, err := sessions.From(context)
		if err != nil {
			return err
		}
		if err := current.Destroy(); err != nil {
			return err
		}
		return context.NoContent(http.StatusNoContent)
	})
	app.Get("/me", func(context *vial.Context) error {
		identity, _ := identities.From(context)
		return context.JSON(http.StatusOK, map[string]string{"subject": identity.Subject})
	}, vial.RouteMiddleware(identities.Require()))
	app.Get("/admin", func(context *vial.Context) error {
		return context.NoContent(http.StatusNoContent)
	}, vial.RouteMiddleware(identities.Require("admin")))
	return app, sessions, nil
}

func refreshSessionKeys(manager *session.Manager, path string) vial.Task {
	return func(context context.Context) error {
		ticker := time.NewTicker(keyRefreshEvery)
		defer ticker.Stop()
		for {
			select {
			case <-context.Done():
				return context.Err()
			case <-ticker.C:
				if err := reloadSessionKeys(manager, path); err != nil {
					slog.Warn("keep current session keys", "error", err)
					continue
				}
				slog.Info("session keys refreshed")
			}
		}
	}
}

func reloadSessionKeys(manager *session.Manager, path string) error {
	keys, err := readSessionKeys(path)
	if err != nil {
		return err
	}
	return manager.ReplaceKeys(keys...)
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
