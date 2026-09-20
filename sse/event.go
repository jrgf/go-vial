package sse

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
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
