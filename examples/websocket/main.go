package main

import (
	"context"
	"crypto/subtle"
	"log/slog"
	"net/http"
	"os"

	"github.com/coder/websocket"
	"github.com/jrgf/go-vial"
	"github.com/jrgf/go-vial/vialws"
)

const (
	websocketTokenEnvironment = "VIAL_WEBSOCKET_TOKEN"
	maximumMessageBytes       = 64 << 10
)

func main() {
	token := os.Getenv(websocketTokenEnvironment)
	if token == "" {
		slog.Error(websocketTokenEnvironment + " is required")
		os.Exit(2)
	}
	app, err := newApp(token)
	if err != nil {
		slog.Error("create application", "error", err)
		os.Exit(1)
	}
	if err := app.Run(context.Background(), ":8080"); err != nil {
		slog.Error("application stopped with an error", "error", err)
		os.Exit(1)
	}
}

func newApp(token string) (*vial.App, error) {
	handler, err := vialws.NewHandler(vialws.Config{
		Handler:   echo,
		ReadLimit: maximumMessageBytes,
	})
	if err != nil {
		return nil, err
	}
	app := vial.New()
	app.HandleHTTP(
		"GET /ws",
		handler,
		vial.RouteMiddleware(authenticate(token)),
	)
	return app, nil
}

func authenticate(token string) vial.Middleware {
	want := []byte("Bearer " + token)
	return func(next vial.Handler) vial.Handler {
		return func(contextValue *vial.Context) error {
			if subtle.ConstantTimeCompare([]byte(contextValue.Header("Authorization")), want) != 1 {
				return vial.Unauthorized("unauthorized", "A valid bearer token is required")
			}
			return next(contextValue)
		}
	}
}

func echo(connectionContext context.Context, connection *websocket.Conn, _ *http.Request) {
	for {
		messageType, message, err := connection.Read(connectionContext)
		if err != nil {
			return
		}
		if err := connection.Write(connectionContext, messageType, message); err != nil {
			return
		}
	}
}
