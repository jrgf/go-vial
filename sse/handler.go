package sse

import (
	"errors"
	"net/http"
	"time"
)

// Handler returns an HTTP event-stream handler. It subscribes each request to
// the hub, emits heartbeat comments, flushes every block, and stops on request
// cancellation.
func (hub *Hub) Handler() http.Handler { return http.HandlerFunc(hub.serveHTTP) }

func (hub *Hub) serveHTTP(writer http.ResponseWriter, request *http.Request) {
	if !hub.startStream(writer) {
		return
	}
	events := hub.Subscribe(request.Context())
	heartbeat := time.NewTicker(hub.heartbeat)
	defer heartbeat.Stop()
	for {
		select {
		case <-request.Context().Done():
			return
		case event, open := <-events:
			if !open || hub.write(writer, func() error { return WriteEvent(writer, event) }) != nil {
				return
			}
		case <-heartbeat.C:
			if hub.write(writer, func() error { return WriteComment(writer, "heartbeat") }) != nil {
				return
			}
		}
	}
}

func (hub *Hub) startStream(writer http.ResponseWriter) bool {
	header := writer.Header()
	header.Set("Content-Type", "text/event-stream")
	header.Set("Cache-Control", "no-cache")
	header.Set("X-Content-Type-Options", "nosniff")
	started := false
	err := hub.write(writer, func() error { started = true; writer.WriteHeader(http.StatusOK); return nil })
	if err != nil && !started {
		http.Error(writer, "stream unavailable", http.StatusInternalServerError)
	}
	return err == nil
}

func (hub *Hub) write(writer http.ResponseWriter, write func() error) (err error) {
	controller := http.NewResponseController(writer)
	if err := streamDeadline(controller, time.Now().Add(hub.writeTimeout)); err != nil {
		return err
	}
	defer func() { err = errors.Join(err, streamDeadline(controller, time.Time{})) }()
	if err := write(); err != nil {
		return err
	}
	return controller.Flush()
}

func streamDeadline(controller *http.ResponseController, deadline time.Time) error {
	err := controller.SetWriteDeadline(deadline)
	if errors.Is(err, http.ErrNotSupported) {
		return nil
	}
	return err
}
