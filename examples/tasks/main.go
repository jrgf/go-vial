package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/jrgf/go-vial"
)

func main() {
	if err := newApp(heartbeat).Run(context.Background(), ":8080"); err != nil {
		slog.Error("application stopped with an error", "error", err)
		os.Exit(1)
	}
}

func newApp(worker vial.Task) *vial.App {
	app := vial.New()
	app.Go("heartbeat", worker, vial.NonCritical())
	app.Get("/", func(context *vial.Context) error {
		return context.Text(http.StatusOK, "tasks running")
	})
	return app
}

func heartbeat(contextValue context.Context) error {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-contextValue.Done():
			return contextValue.Err()
		case <-ticker.C:
			slog.Info("heartbeat")
		}
	}
}
