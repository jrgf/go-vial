package vialws

import (
	"context"
	"errors"
	"net/http"

	"github.com/coder/websocket"
)

// DefaultReadLimit is the maximum message size accepted by default.
const DefaultReadLimit int64 = 64 << 10

// Handler serves one accepted WebSocket connection. It should block until the
// connection closes or the context is canceled. The request retains access to
// Vial request state through vial.ContextFromRequest.
type Handler func(context.Context, *websocket.Conn, *http.Request)

// Config configures a WebSocket handler.
type Config struct {
	// Handler receives each accepted connection.
	Handler Handler
	// AcceptOptions controls subprotocol, origin, and compression behavior.
	AcceptOptions websocket.AcceptOptions
	// ReadLimit bounds each incoming message. Zero uses 64 KiB.
	ReadLimit int64
}

// NewHandler creates a standard HTTP handler with same-origin checks, a bounded
// read limit, and graceful closure when the request context is canceled.
func NewHandler(config Config) (http.Handler, error) {
	if config.Handler == nil {
		return nil, errors.New("vialws: handler cannot be nil")
	}
	if config.ReadLimit < 0 {
		return nil, errors.New("vialws: read limit cannot be negative")
	}
	if config.ReadLimit == 0 {
		config.ReadLimit = DefaultReadLimit
	}
	config.AcceptOptions.Subprotocols = append([]string(nil), config.AcceptOptions.Subprotocols...)
	config.AcceptOptions.OriginPatterns = append([]string(nil), config.AcceptOptions.OriginPatterns...)

	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		connection, err := websocket.Accept(writer, request, &config.AcceptOptions)
		if err != nil {
			return
		}
		connection.SetReadLimit(config.ReadLimit)

		connectionContext, cancelConnection := context.WithCancel(context.WithoutCancel(request.Context()))
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
			_ = connection.CloseNow()
		}()

		config.Handler(connectionContext, connection, request)
	}), nil
}
