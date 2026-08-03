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
	capabilities http.ResponseWriter
	status       int
	bytes        int64
	wroteHeader  bool
}

func newResponseWriter(writer http.ResponseWriter) *ResponseWriter {
	response := &ResponseWriter{ResponseWriter: writer}
	response.capabilities = preserveResponseWriterCapabilities(response)
	return response
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

type responseWriterCore interface {
	http.ResponseWriter
	Unwrap() http.ResponseWriter
}

type responseFlusher struct {
	writer  *ResponseWriter
	flusher http.Flusher
}

func (flusher responseFlusher) Flush() {
	if !flusher.writer.wroteHeader {
		flusher.writer.WriteHeader(http.StatusOK)
	}
	flusher.flusher.Flush()
}

type responseHijacker struct {
	hijacker http.Hijacker
}

func (hijacker responseHijacker) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return hijacker.hijacker.Hijack()
}

type responsePusher struct {
	pusher http.Pusher
}

func (pusher responsePusher) Push(target string, options *http.PushOptions) error {
	return pusher.pusher.Push(target, options)
}

type responseReaderFrom struct {
	writer     *ResponseWriter
	readerFrom io.ReaderFrom
}

func (readerFrom responseReaderFrom) ReadFrom(reader io.Reader) (int64, error) {
	if !readerFrom.writer.wroteHeader {
		readerFrom.writer.WriteHeader(http.StatusOK)
	}
	written, err := readerFrom.readerFrom.ReadFrom(reader)
	readerFrom.writer.bytes += written
	return written, err
}

func preserveResponseWriterCapabilities(writer *ResponseWriter) http.ResponseWriter {
	underlying := writer.ResponseWriter
	flusher, hasFlusher := underlying.(http.Flusher)
	hijacker, hasHijacker := underlying.(http.Hijacker)
	readerFrom, hasReaderFrom := underlying.(io.ReaderFrom)
	pusher, hasPusher := underlying.(http.Pusher)
	mask := 0
	if hasFlusher {
		mask |= 1
	}
	if hasHijacker {
		mask |= 2
	}
	if hasReaderFrom {
		mask |= 4
	}
	if hasPusher {
		mask |= 8
	}

	flush := responseFlusher{writer: writer, flusher: flusher}
	hijack := responseHijacker{hijacker: hijacker}
	read := responseReaderFrom{writer: writer, readerFrom: readerFrom}
	push := responsePusher{pusher: pusher}
	core := responseWriterCore(writer)
	switch mask {
	case 1:
		return struct {
			responseWriterCore
			http.Flusher
		}{core, flush}
	case 2:
		return struct {
			responseWriterCore
			http.Hijacker
		}{core, hijack}
	case 3:
		return struct {
			responseWriterCore
			http.Flusher
			http.Hijacker
		}{core, flush, hijack}
	case 4:
		return struct {
			responseWriterCore
			io.ReaderFrom
		}{core, read}
	case 5:
		return struct {
			responseWriterCore
			http.Flusher
			io.ReaderFrom
		}{core, flush, read}
	case 6:
		return struct {
			responseWriterCore
			http.Hijacker
			io.ReaderFrom
		}{core, hijack, read}
	case 7:
		return struct {
			responseWriterCore
			http.Flusher
			http.Hijacker
			io.ReaderFrom
		}{core, flush, hijack, read}
	case 8:
		return struct {
			responseWriterCore
			http.Pusher
		}{core, push}
	case 9:
		return struct {
			responseWriterCore
			http.Flusher
			http.Pusher
		}{core, flush, push}
	case 10:
		return struct {
			responseWriterCore
			http.Hijacker
			http.Pusher
		}{core, hijack, push}
	case 11:
		return struct {
			responseWriterCore
			http.Flusher
			http.Hijacker
			http.Pusher
		}{core, flush, hijack, push}
	case 12:
		return struct {
			responseWriterCore
			io.ReaderFrom
			http.Pusher
		}{core, read, push}
	case 13:
		return struct {
			responseWriterCore
			http.Flusher
			io.ReaderFrom
			http.Pusher
		}{core, flush, read, push}
	case 14:
		return struct {
			responseWriterCore
			http.Hijacker
			io.ReaderFrom
			http.Pusher
		}{core, hijack, read, push}
	case 15:
		return struct {
			responseWriterCore
			http.Flusher
			http.Hijacker
			io.ReaderFrom
			http.Pusher
		}{core, flush, hijack, read, push}
	default:
		return writer
	}
}
