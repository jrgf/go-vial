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

func (writer *ResponseWriter) WriteHeader(status int) {
	if writer.wroteHeader {
		return
	}
	writer.status = status
	writer.wroteHeader = true
	writer.ResponseWriter.WriteHeader(status)
}

func (writer *ResponseWriter) Write(data []byte) (int, error) {
	if !writer.wroteHeader {
		writer.WriteHeader(http.StatusOK)
	}

	written, err := writer.ResponseWriter.Write(data)
	writer.bytes += int64(written)
	return written, err
}

func (writer *ResponseWriter) Status() int {
	if writer.status == 0 {
		return http.StatusOK
	}
	return writer.status
}

func (writer *ResponseWriter) BytesWritten() int64 {
	return writer.bytes
}

func (writer *ResponseWriter) Committed() bool {
	return writer.wroteHeader
}

func (writer *ResponseWriter) Unwrap() http.ResponseWriter {
	return writer.ResponseWriter
}

func (writer *ResponseWriter) Flush() {
	if !writer.wroteHeader {
		writer.WriteHeader(http.StatusOK)
	}
	_ = http.NewResponseController(writer.ResponseWriter).Flush()
}

func (writer *ResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return http.NewResponseController(writer.ResponseWriter).Hijack()
}

func (writer *ResponseWriter) Push(target string, options *http.PushOptions) error {
	pusher, ok := writer.ResponseWriter.(http.Pusher)
	if !ok {
		return http.ErrNotSupported
	}
	return pusher.Push(target, options)
}

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
