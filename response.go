package vial

import (
	"bufio"
	"io"
	"net"
	"net/http"
)

// ResponseWriter tracks response state while retaining access to the underlying
// standard-library writer through Unwrap.
type ResponseWriter struct {
	http.ResponseWriter
	status      int
	bytes       int64
	wroteHeader bool
}

func newResponseWriter(writer http.ResponseWriter) *ResponseWriter {
	return &ResponseWriter{ResponseWriter: writer}
}

// WriteHeader records and writes the first response status.
func (writer *ResponseWriter) WriteHeader(status int) {
	if writer.wroteHeader {
		return
	}
	writer.status = status
	writer.wroteHeader = true
	writer.ResponseWriter.WriteHeader(status)
}

// Write writes response data and records its size.
func (writer *ResponseWriter) Write(data []byte) (int, error) {
	if !writer.wroteHeader {
		writer.WriteHeader(http.StatusOK)
	}

	written, err := writer.ResponseWriter.Write(data)
	writer.bytes += int64(written)
	return written, err
}

// Status returns the written status or 200 before commitment.
func (writer *ResponseWriter) Status() int {
	if writer.status == 0 {
		return http.StatusOK
	}
	return writer.status
}

// BytesWritten returns the response body bytes written so far.
func (writer *ResponseWriter) BytesWritten() int64 {
	return writer.bytes
}

// Committed reports whether response headers were written.
func (writer *ResponseWriter) Committed() bool {
	return writer.wroteHeader
}

// Unwrap returns the underlying standard-library response writer.
func (writer *ResponseWriter) Unwrap() http.ResponseWriter {
	return writer.ResponseWriter
}

// Flush implements http.Flusher when supported by the underlying writer.
func (writer *ResponseWriter) Flush() {
	if !writer.wroteHeader {
		writer.WriteHeader(http.StatusOK)
	}
	_ = http.NewResponseController(writer.ResponseWriter).Flush()
}

// Hijack delegates connection ownership to the underlying http.Hijacker.
func (writer *ResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return http.NewResponseController(writer.ResponseWriter).Hijack()
}

// Push delegates an HTTP/2 server push to the underlying http.Pusher.
func (writer *ResponseWriter) Push(target string, options *http.PushOptions) error {
	pusher, ok := writer.ResponseWriter.(http.Pusher)
	if !ok {
		return http.ErrNotSupported
	}
	return pusher.Push(target, options)
}

// ReadFrom copies reader to the response while recording its size.
func (writer *ResponseWriter) ReadFrom(reader io.Reader) (int64, error) {
	if !writer.wroteHeader {
		writer.WriteHeader(http.StatusOK)
	}

	if readerFrom, ok := writer.ResponseWriter.(io.ReaderFrom); ok {
		written, err := readerFrom.ReadFrom(reader)
		writer.bytes += written
		return written, err
	}

	written, err := io.Copy(struct{ io.Writer }{writer.ResponseWriter}, reader)
	writer.bytes += written
	return written, err
}
