package vial

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"reflect"
	"strings"
	"sync"
)

// Context contains request-scoped framework state while retaining direct access
// to the standard net/http request and response writer.
type Context struct {
	app         *App
	request     *http.Request
	response    *ResponseWriter
	route       *Route
	routeErr    error
	logger      *slog.Logger
	bodyLimited bool

	valuesMu sync.RWMutex
	values   map[string]any
}

func newContext(app *App, writer *ResponseWriter, request *http.Request) *Context {
	return &Context{
		app:      app,
		request:  request,
		response: writer,
		logger:   app.config.logger,
		values:   make(map[string]any),
	}
}

func (context *Context) App() *App {
	return context.app
}

func (context *Context) Request() *http.Request {
	return context.request
}

func (context *Context) Response() http.ResponseWriter {
	return context.response
}

func (context *Context) Route() *Route {
	return context.route
}

func (context *Context) Param(name string) string {
	return context.request.PathValue(name)
}

func (context *Context) Query(name string) string {
	return context.request.URL.Query().Get(name)
}

func (context *Context) Header(name string) string {
	return context.request.Header.Get(name)
}

func (context *Context) Logger() *slog.Logger {
	if context.logger == nil {
		return slog.Default()
	}
	return context.logger
}

func (context *Context) SetLogger(logger *slog.Logger) {
	if logger != nil {
		context.logger = logger
	}
}

func (context *Context) Set(key string, value any) {
	context.valuesMu.Lock()
	context.values[key] = value
	context.valuesMu.Unlock()
}

func (context *Context) Get(key string) (any, bool) {
	context.valuesMu.RLock()
	value, ok := context.values[key]
	context.valuesMu.RUnlock()
	return value, ok
}

func (context *Context) Status() int {
	return context.response.Status()
}

func (context *Context) BytesWritten() int64 {
	return context.response.BytesWritten()
}

func (context *Context) Committed() bool {
	return context.response.Committed()
}

func (context *Context) JSON(status int, value any) error {
	if context.Committed() {
		return errors.New("vial: response already committed")
	}

	payload, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode JSON response: %w", err)
	}
	payload = append(payload, '\n')

	context.response.Header().Set("Content-Type", "application/json; charset=utf-8")
	context.response.WriteHeader(status)
	_, err = context.response.Write(payload)
	return err
}

func (context *Context) Text(status int, value string) error {
	if context.Committed() {
		return errors.New("vial: response already committed")
	}

	context.response.Header().Set("Content-Type", "text/plain; charset=utf-8")
	context.response.WriteHeader(status)
	_, err := io.WriteString(context.response, value)
	return err
}

func (context *Context) NoContent(status int) error {
	if context.Committed() {
		return errors.New("vial: response already committed")
	}
	context.response.WriteHeader(status)
	return nil
}

func (context *Context) Redirect(status int, location string) error {
	if context.Committed() {
		return errors.New("vial: response already committed")
	}
	http.Redirect(context.response, context.request, location, status)
	return nil
}

// BindJSON decodes a single JSON value into destination using the application's
// body limit and unknown-field policy.
func (context *Context) BindJSON(destination any) error {
	if destination == nil {
		return BadRequest("invalid_destination", "JSON destination cannot be nil")
	}

	value := reflect.ValueOf(destination)
	if value.Kind() != reflect.Pointer || value.IsNil() {
		return BadRequest("invalid_destination", "JSON destination must be a non-nil pointer")
	}

	if contentType := context.request.Header.Get("Content-Type"); contentType != "" {
		mediaType, _, err := mime.ParseMediaType(contentType)
		if err != nil || (mediaType != "application/json" && !strings.HasSuffix(mediaType, "+json")) {
			return UnsupportedMediaType(
				"unsupported_media_type",
				"Content-Type must be application/json",
			)
		}
	}

	context.limitBody()
	decoder := json.NewDecoder(context.request.Body)
	if context.app.config.disallowUnknownJSONFields {
		decoder.DisallowUnknownFields()
	}

	if err := decoder.Decode(destination); err != nil {
		var maxBytesErr *http.MaxBytesError
		var typeErr *json.UnmarshalTypeError
		switch {
		case errors.Is(err, io.EOF):
			return BadRequest("empty_body", "Request body must contain JSON")
		case errors.As(err, &maxBytesErr):
			return RequestEntityTooLarge(
				"request_body_too_large",
				"Request body exceeds the configured limit",
			)
		case errors.As(err, &typeErr) && typeErr.Field != "":
			return bindingFault(
				"invalid_json",
				"Request body contains invalid JSON",
				&BindingError{Source: "json", Field: typeErr.Field, Cause: err},
			)
		default:
			return WrapHTTPError(
				http.StatusBadRequest,
				"invalid_json",
				"Request body contains invalid JSON",
				err,
			)
		}
	}

	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return BadRequest("multiple_json_values", "Request body must contain one JSON value")
		}
		return WrapHTTPError(
			http.StatusBadRequest,
			"invalid_json",
			"Request body contains invalid trailing data",
			err,
		)
	}

	return nil
}
