package vial

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestResponseWriterTracksStreamingResponse(t *testing.T) {
	recorder := httptest.NewRecorder()
	writer := newResponseWriter(recorder)

	written, err := writer.ReadFrom(strings.NewReader("streamed"))
	if err != nil {
		t.Fatalf("read from: %v", err)
	}
	if written != 8 || writer.BytesWritten() != 8 || writer.Status() != http.StatusOK || !writer.Committed() {
		t.Fatalf("unexpected response state: written=%d bytes=%d status=%d committed=%v", written, writer.BytesWritten(), writer.Status(), writer.Committed())
	}
	if writer.Unwrap() != recorder || recorder.Body.String() != "streamed" {
		t.Fatal("response writer did not retain the underlying writer")
	}
	if err := writer.Push("/asset", nil); !errors.Is(err, http.ErrNotSupported) {
		t.Fatalf("unsupported push returned %v", err)
	}
	if _, _, err := writer.Hijack(); err == nil {
		t.Fatal("expected recorder hijack to be unsupported")
	}
}

func TestResponseWriterFlushAndPush(t *testing.T) {
	recorder := httptest.NewRecorder()
	writer := newResponseWriter(recorder)
	writer.Flush()
	if !recorder.Flushed || writer.Status() != http.StatusOK {
		t.Fatal("flush did not commit the response")
	}

	pusher := &pushRecorder{ResponseRecorder: httptest.NewRecorder()}
	if err := newResponseWriter(pusher).Push("/asset", &http.PushOptions{Method: http.MethodGet}); err != nil {
		t.Fatalf("push: %v", err)
	}
	if pusher.target != "/asset" {
		t.Fatalf("unexpected push target %q", pusher.target)
	}
}

type pushRecorder struct {
	*httptest.ResponseRecorder
	target string
}

func (recorder *pushRecorder) Push(target string, _ *http.PushOptions) error {
	recorder.target = target
	return nil
}
