package sse

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	// DefaultBuffer is the number of pending events allowed per subscriber.
	DefaultBuffer = 16
	// DefaultHeartbeat is the interval between heartbeat comments.
	DefaultHeartbeat = 15 * time.Second
	// DefaultWriteTimeout bounds each event or heartbeat write.
	DefaultWriteTimeout = 10 * time.Second
)

// SlowConsumerPolicy controls what happens when a subscriber buffer is full.
type SlowConsumerPolicy uint8

const (
	// DisconnectSlowConsumer closes a subscriber whose buffer is full.
	DisconnectSlowConsumer SlowConsumerPolicy = iota
	// DropEvent keeps the subscriber and drops the new event for that subscriber.
	DropEvent
)

// Event is one Server-Sent Event.
type Event struct {
	// ID identifies the event for Last-Event-ID reconnection.
	ID string
	// Name selects the browser event type. An empty name dispatches "message".
	Name string
	// Retry asks the client to wait this long before reconnecting.
	Retry time.Duration
	// Data is copied for each subscriber by Hub.Publish.
	Data []byte
}

// JSON creates an event whose data is a JSON value.
func JSON(name string, value any) (Event, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return Event{}, fmt.Errorf("encode SSE JSON: %w", err)
	}
	event := Event{Name: name, Data: data}
	if err := validateEvent(event); err != nil {
		return Event{}, err
	}
	return event, nil
}

// WriteEvent writes one complete event, including its terminating blank line.
func WriteEvent(writer io.Writer, event Event) error {
	if writer == nil {
		return errors.New("sse: writer cannot be nil")
	}
	if err := validateEvent(event); err != nil {
		return err
	}

	var buffer bytes.Buffer
	if event.ID != "" {
		fmt.Fprintf(&buffer, "id: %s\n", event.ID)
	}
	if event.Name != "" {
		fmt.Fprintf(&buffer, "event: %s\n", event.Name)
	}
	if event.Retry > 0 {
		fmt.Fprintf(&buffer, "retry: %d\n", max(int64(1), event.Retry.Milliseconds()))
	}
	writeLines(&buffer, "data", event.Data)
	buffer.WriteByte('\n')
	return writeAll(writer, buffer.Bytes())
}

// WriteComment writes one comment block. Comments are commonly used as
// heartbeats because browsers do not dispatch them as events.
func WriteComment(writer io.Writer, comment string) error {
	if writer == nil {
		return errors.New("sse: writer cannot be nil")
	}
	var buffer bytes.Buffer
	writeLines(&buffer, ":", []byte(comment))
	buffer.WriteByte('\n')
	return writeAll(writer, buffer.Bytes())
}

func validateEvent(event Event) error {
	if strings.ContainsAny(event.ID, "\r\n\x00") {
		return errors.New("sse: event ID cannot contain a newline or NUL byte")
	}
	if strings.ContainsAny(event.Name, "\r\n") {
		return errors.New("sse: event name cannot contain a newline")
	}
	if event.Retry < 0 {
		return errors.New("sse: retry cannot be negative")
	}
	return nil
}

func writeLines(buffer *bytes.Buffer, field string, value []byte) {
	value = bytes.ReplaceAll(value, []byte("\r\n"), []byte("\n"))
	value = bytes.ReplaceAll(value, []byte("\r"), []byte("\n"))
	for line := range bytes.SplitSeq(value, []byte("\n")) {
		if field == ":" {
			buffer.WriteByte(':')
			if len(line) > 0 {
				buffer.WriteByte(' ')
			}
		} else {
			buffer.WriteString(field)
			buffer.WriteString(": ")
		}
		buffer.Write(line)
		buffer.WriteByte('\n')
	}
}

func writeAll(writer io.Writer, data []byte) error {
	written, err := writer.Write(data)
	if err != nil {
		return fmt.Errorf("write SSE event: %w", err)
	}
	if written != len(data) {
		return fmt.Errorf("write SSE event: %w", io.ErrShortWrite)
	}
	return nil
}

// HubConfig configures a Hub.
type HubConfig struct {
	// SubscriberBuffer bounds pending events for each subscriber. Zero uses 16.
	SubscriberBuffer int
	// SlowConsumer chooses disconnect or per-subscriber event dropping.
	SlowConsumer SlowConsumerPolicy
	// Heartbeat controls comment frequency. Zero uses 15 seconds.
	Heartbeat time.Duration
	// WriteTimeout bounds each network write. Zero uses 10 seconds.
	WriteTimeout time.Duration
}

// Hub fans events out to bounded subscriber channels.
type Hub struct {
	mu           sync.Mutex
	subscribers  map[*subscriber]struct{}
	buffer       int
	policy       SlowConsumerPolicy
	heartbeat    time.Duration
	writeTimeout time.Duration
	closed       bool
}

type subscriber struct {
	events chan Event
	done   chan struct{}
}

// NewHub creates a bounded event hub.
func NewHub(config HubConfig) (*Hub, error) {
	if config.SubscriberBuffer < 0 {
		return nil, errors.New("sse: subscriber buffer cannot be negative")
	}
	if config.SubscriberBuffer == 0 {
		config.SubscriberBuffer = DefaultBuffer
	}
	if config.Heartbeat < 0 {
		return nil, errors.New("sse: heartbeat cannot be negative")
	}
	if config.Heartbeat == 0 {
		config.Heartbeat = DefaultHeartbeat
	}
	if config.WriteTimeout < 0 {
		return nil, errors.New("sse: write timeout cannot be negative")
	}
	if config.WriteTimeout == 0 {
		config.WriteTimeout = DefaultWriteTimeout
	}
	if config.SlowConsumer != DisconnectSlowConsumer && config.SlowConsumer != DropEvent {
		return nil, errors.New("sse: invalid slow-consumer policy")
	}
	return &Hub{
		subscribers:  make(map[*subscriber]struct{}),
		buffer:       config.SubscriberBuffer,
		policy:       config.SlowConsumer,
		heartbeat:    config.Heartbeat,
		writeTimeout: config.WriteTimeout,
	}, nil
}

// Subscribe returns a bounded event channel that closes when the context is
// canceled, the subscriber falls behind under DisconnectSlowConsumer, or the
// hub closes.
func (hub *Hub) Subscribe(contextValue context.Context) <-chan Event {
	if contextValue == nil {
		contextValue = context.Background()
	}
	subscription := &subscriber{
		events: make(chan Event, hub.buffer),
		done:   make(chan struct{}),
	}
	if contextValue.Err() != nil {
		close(subscription.events)
		close(subscription.done)
		return subscription.events
	}

	hub.mu.Lock()
	if hub.closed {
		hub.mu.Unlock()
		close(subscription.events)
		close(subscription.done)
		return subscription.events
	}
	hub.subscribers[subscription] = struct{}{}
	hub.mu.Unlock()

	go func() {
		select {
		case <-contextValue.Done():
			hub.remove(subscription)
		case <-subscription.done:
		}
	}()
	return subscription.events
}

// Publish snapshots an event and offers it to every current subscriber. It
// returns the number of subscribers that accepted the event.
func (hub *Hub) Publish(event Event) (int, error) {
	if err := validateEvent(event); err != nil {
		return 0, err
	}
	hub.mu.Lock()
	defer hub.mu.Unlock()
	if hub.closed {
		return 0, nil
	}
	delivered := 0
	for subscription := range hub.subscribers {
		snapshot := event
		snapshot.Data = bytes.Clone(event.Data)
		select {
		case subscription.events <- snapshot:
			delivered++
		default:
			if hub.policy == DisconnectSlowConsumer {
				delete(hub.subscribers, subscription)
				close(subscription.done)
				close(subscription.events)
			}
		}
	}
	return delivered, nil
}

// Close disconnects every subscriber. Future subscriptions are closed and
// future publications are ignored.
func (hub *Hub) Close() {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	if hub.closed {
		return
	}
	hub.closed = true
	for subscription := range hub.subscribers {
		delete(hub.subscribers, subscription)
		close(subscription.done)
		close(subscription.events)
	}
}

func (hub *Hub) remove(subscription *subscriber) {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	if _, exists := hub.subscribers[subscription]; !exists {
		return
	}
	delete(hub.subscribers, subscription)
	close(subscription.done)
	close(subscription.events)
}

// Handler returns an HTTP event-stream handler. It subscribes each request to
// the hub, emits heartbeat comments, flushes every block, and stops on request
// cancellation.
func (hub *Hub) Handler() http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		controller := http.NewResponseController(writer)
		setDeadline := func() bool {
			err := controller.SetWriteDeadline(time.Now().Add(hub.writeTimeout))
			return err == nil || errors.Is(err, http.ErrNotSupported)
		}
		if !setDeadline() {
			http.Error(writer, "stream unavailable", http.StatusInternalServerError)
			return
		}

		header := writer.Header()
		header.Set("Content-Type", "text/event-stream")
		header.Set("Cache-Control", "no-cache")
		header.Set("X-Content-Type-Options", "nosniff")
		writer.WriteHeader(http.StatusOK)
		if err := controller.Flush(); err != nil {
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
				if !open || !setDeadline() || WriteEvent(writer, event) != nil || controller.Flush() != nil {
					return
				}
			case <-heartbeat.C:
				if !setDeadline() || WriteComment(writer, "heartbeat") != nil || controller.Flush() != nil {
					return
				}
			}
		}
	})
}
