package main

import (
	"context"
	"crypto/subtle"
	"log/slog"
	"net/http"
	"os"

	"github.com/coder/websocket"
	"github.com/jrgf/go-vial"
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
	if err := newApp(token).Run(context.Background(), ":8080"); err != nil {
		slog.Error("application stopped with an error", "error", err)
		os.Exit(1)
	}
}

func newApp(token string) *vial.App {
	app := vial.New()
	app.HandleHTTP(
		"GET /ws",
		http.HandlerFunc(echo),
		vial.RouteMiddleware(authenticate(token)),
	)
	return app
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

func echo(writer http.ResponseWriter, request *http.Request) {
	connection, err := websocket.Accept(writer, request, nil)
	if err != nil {
		return
	}
	defer func() { _ = connection.CloseNow() }()
	connection.SetReadLimit(maximumMessageBytes)

	connectionContext, cancelConnection := context.WithCancel(context.Background())
	watcherDone := make(chan struct{})
	go func() {
		defer close(watcherDone)
		select {
		case <-request.Context().Done():
			_ = connection.Close(websocket.StatusGoingAway, "server shutting down")
			cancelConnection()
		case <-connectionContext.Done():
		}
	}()
	defer func() {
		cancelConnection()
		<-watcherDone
	}()

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
