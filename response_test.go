package vial

import (
	"bufio"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestResponseWriterTracksStreamingResponse(t *testing.T) {
	recorder := &readerFromWriter{ResponseWriter: httptest.NewRecorder()}
	writer := newResponseWriter(recorder)

	readerFrom, ok := writer.capabilities.(io.ReaderFrom)
	if !ok {
		t.Fatal("wrapped writer did not preserve io.ReaderFrom")
	}
	written, err := readerFrom.ReadFrom(strings.NewReader("streamed"))
	if err != nil {
		t.Fatalf("read from: %v", err)
	}
	if written != 8 || writer.BytesWritten() != 8 || writer.Status() != http.StatusOK || !writer.Committed() {
		t.Fatalf("unexpected response state: written=%d bytes=%d status=%d committed=%v", written, writer.BytesWritten(), writer.Status(), writer.Committed())
	}
	if writer.Unwrap() != recorder || recorder.ResponseWriter.(*httptest.ResponseRecorder).Body.String() != "streamed" {
		t.Fatal("response writer did not retain the underlying writer")
	}
	if _, ok := writer.capabilities.(http.Pusher); ok {
		t.Fatal("wrapped writer unexpectedly implements http.Pusher")
	}
	if _, ok := writer.capabilities.(http.Hijacker); ok {
		t.Fatal("wrapped writer unexpectedly implements http.Hijacker")
	}
}

func TestResponseWriterFlushAndPush(t *testing.T) {
	recorder := httptest.NewRecorder()
	writer := newResponseWriter(recorder)
	flusher, ok := writer.capabilities.(http.Flusher)
	if !ok {
		t.Fatal("wrapped writer did not preserve http.Flusher")
	}
	flusher.Flush()
	flusher.Flush()
	if !recorder.Flushed || writer.Status() != http.StatusOK {
		t.Fatal("flush did not commit the response")
	}

	pusher := &pushRecorder{ResponseRecorder: httptest.NewRecorder()}
	wrappedPusher, ok := newResponseWriter(pusher).capabilities.(http.Pusher)
	if !ok {
		t.Fatal("wrapped writer did not preserve http.Pusher")
	}
	if err := wrappedPusher.Push("/asset", &http.PushOptions{Method: http.MethodGet}); err != nil {
		t.Fatalf("push: %v", err)
	}
	if pusher.target != "/asset" {
		t.Fatalf("unexpected push target %q", pusher.target)
	}
}

func TestResponseWriterPreservesOnlyUnderlyingCapabilities(t *testing.T) {
	plain := &plainWriter{header: make(http.Header)}
	tests := []struct {
		name                          string
		writer                        http.ResponseWriter
		flush, hijack, readFrom, push bool
	}{
		{name: "none", writer: plain},
		{name: "flush", writer: &flushWriter{ResponseWriter: plain}, flush: true},
		{name: "hijack", writer: &hijackWriter{ResponseWriter: plain}, hijack: true},
		{name: "read from", writer: &readerFromWriter{ResponseWriter: plain}, readFrom: true},
		{name: "push", writer: &pushWriter{ResponseWriter: plain}, push: true},
		{name: "all", writer: &allCapabilitiesWriter{ResponseWriter: plain}, flush: true, hijack: true, readFrom: true, push: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			wrapped := newResponseWriter(test.writer).capabilities
			_, flush := wrapped.(http.Flusher)
			_, hijack := wrapped.(http.Hijacker)
			_, readFrom := wrapped.(io.ReaderFrom)
			_, push := wrapped.(http.Pusher)
			if flush != test.flush || hijack != test.hijack || readFrom != test.readFrom || push != test.push {
				t.Fatalf("capabilities: flush=%v hijack=%v readFrom=%v push=%v", flush, hijack, readFrom, push)
			}
			if _, ok := any(wrapped).(interface{ Unwrap() http.ResponseWriter }); !ok {
				t.Fatal("wrapped writer does not support http.ResponseController unwrapping")
			}
		})
	}
}

type plainWriter struct {
	header http.Header
	status int
	body   strings.Builder
}

func (writer *plainWriter) Header() http.Header            { return writer.header }
func (writer *plainWriter) WriteHeader(status int)         { writer.status = status }
func (writer *plainWriter) Write(data []byte) (int, error) { return writer.body.Write(data) }

type flushWriter struct{ http.ResponseWriter }

func (*flushWriter) Flush() {}

type hijackWriter struct{ http.ResponseWriter }

func (*hijackWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return nil, nil, errors.New("test hijack")
}

type readerFromWriter struct{ http.ResponseWriter }

func (writer *readerFromWriter) ReadFrom(reader io.Reader) (int64, error) {
	return io.Copy(writer.ResponseWriter, reader)
}

type pushWriter struct{ http.ResponseWriter }

func (*pushWriter) Push(string, *http.PushOptions) error { return nil }

type allCapabilitiesWriter struct{ http.ResponseWriter }

func (*allCapabilitiesWriter) Flush() {}
func (*allCapabilitiesWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return nil, nil, errors.New("test hijack")
}
func (writer *allCapabilitiesWriter) ReadFrom(reader io.Reader) (int64, error) {
	return io.Copy(writer.ResponseWriter, reader)
}
func (*allCapabilitiesWriter) Push(string, *http.PushOptions) error { return nil }

type pushRecorder struct {
	*httptest.ResponseRecorder
	target string
}

func (recorder *pushRecorder) Push(target string, _ *http.PushOptions) error {
	recorder.target = target
	return nil
}
