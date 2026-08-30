package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/jrgf/go-vial"
)

type Event struct {
	Time time.Time `json:"time"`
}

func main() {
	if err := newApp(time.Second).Run(context.Background(), ":8080"); err != nil {
		slog.Error("application stopped with an error", "error", err)
		os.Exit(1)
	}
}

func newApp(interval time.Duration) *vial.App {
	app := vial.New()
	app.Get("/events", func(contextValue *vial.Context) error {
		header := contextValue.Response().Header()
		header.Set("Content-Type", "text/event-stream")
		header.Set("Cache-Control", "no-cache")
		header.Set("X-Content-Type-Options", "nosniff")
		if err := contextValue.SetWriteDeadline(time.Time{}); err != nil && !errors.Is(err, http.ErrNotSupported) {
			return err
		}
		contextValue.Response().WriteHeader(http.StatusOK)
		if err := contextValue.Flush(); err != nil {
			return err
		}

		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-contextValue.Request().Context().Done():
				return nil
			case now := <-ticker.C:
				data, err := json.Marshal(Event{Time: now})
				if err != nil {
					return fmt.Errorf("encode event: %w", err)
				}
				if _, err := fmt.Fprintf(contextValue.Response(), "data: %s\n\n", data); err != nil {
					return fmt.Errorf("write event: %w", err)
				}
				if err := contextValue.Flush(); err != nil {
					return err
				}
			}
		}
	})
	return app
}
