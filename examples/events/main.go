package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"

	"github.com/jrgf/go-vial"
)

type Event struct {
	Name string `json:"name"`
}

func main() {
	app := newApp(func(contextValue context.Context, event Event) error {
		slog.InfoContext(contextValue, "event received", "name", event.Name)
		return nil
	})
	if err := app.Run(context.Background(), ":8080"); err != nil {
		slog.Error("application stopped with an error", "error", err)
		os.Exit(1)
	}
}

func newApp(handleEvent func(context.Context, Event) error) *vial.App {
	events := make(chan Event, 128)
	app := vial.New()
	app.Post("/events", func(contextValue *vial.Context) error {
		var event Event
		if err := contextValue.BindJSON(&event); err != nil {
			return err
		}
		select {
		case events <- event:
			return contextValue.NoContent(http.StatusAccepted)
		case <-contextValue.Request().Context().Done():
			return contextValue.Request().Context().Err()
		default:
			return vial.NewHTTPError(
				http.StatusServiceUnavailable,
				"queue_full",
				"Event queue is full",
			)
		}
	})
	app.Go("event-consumer", func(contextValue context.Context) error {
		for {
			select {
			case <-contextValue.Done():
				return contextValue.Err()
			case event := <-events:
				if err := handleEvent(contextValue, event); err != nil {
					return err
				}
			}
		}
	})
	return app
}
