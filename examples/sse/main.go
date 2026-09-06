package main

import (
	"context"
	"log/slog"
	"os"
	"time"

	"github.com/jrgf/go-vial"
	"github.com/jrgf/go-vial/sse"
)

type Event struct {
	Time time.Time `json:"time"`
}

func main() {
	app, err := newApp(time.Second)
	if err != nil {
		slog.Error("create application", "error", err)
		os.Exit(1)
	}
	if err := app.Run(context.Background(), ":8080"); err != nil {
		slog.Error("application stopped with an error", "error", err)
		os.Exit(1)
	}
}

func newApp(interval time.Duration) (*vial.App, error) {
	hub, err := sse.NewHub(sse.HubConfig{})
	if err != nil {
		return nil, err
	}
	app := vial.New()
	app.HandleHTTP("GET /events", hub.Handler())
	app.Go("events", func(contextValue context.Context) error {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		defer hub.Close()
		for {
			select {
			case <-contextValue.Done():
				return nil
			case now := <-ticker.C:
				event, err := sse.JSON("time", Event{Time: now})
				if err != nil {
					return err
				}
				if _, err := hub.Publish(event); err != nil {
					return err
				}
			}
		}
	})
	return app, nil
}
