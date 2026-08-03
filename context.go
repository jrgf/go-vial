package vial

import (
	stdcontext "context"
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
	"time"
)

type requestValuesContextKey struct{}
type stringRequestValueKey string

type requestValues struct {
	mu     sync.RWMutex
	values map[any]any
}

func (values *requestValues) set(key, value any) {
	values.mu.Lock()
	values.values[key] = value
	values.mu.Unlock()
}

func (values *requestValues) get(key any) (any, bool) {
	values.mu.RLock()
	value, ok := values.values[key]
	values.mu.RUnlock()
	return value, ok
}

// ValueKey is a collision-safe request value key. Keep and reuse the pointer
// returned by NewValueKey; identity, not the display name, selects the value.
type ValueKey[T any] struct {
	name string
}

// NewValueKey creates a typed request value key.
func NewValueKey[T any](name string) *ValueKey[T] {
	return &ValueKey[T]{name: name}
}

// Name returns the diagnostic name of the key. Names do not affect identity.
func (key *ValueKey[T]) Name() string {
	return key.name
}

// Set stores a value for the current request. Values are safe for concurrent
// access during the request but should not be retained after it completes.
func (key *ValueKey[T]) Set(context *Context, value T) {
	context.values.set(key, value)
}

// Get returns a typed value from a Vial context.
func (key *ValueKey[T]) Get(context *Context) (T, bool) {
	return requestValue[T](context.values, key)
}

// FromRequest returns the same typed value through the underlying request.
func (key *ValueKey[T]) FromRequest(request *http.Request) (T, bool) {
	var zero T
	if request == nil {
		return zero, false
	}
	values, ok := request.Context().Value(requestValuesContextKey{}).(*requestValues)
	if !ok || values == nil {
		return zero, false
	}
	return requestValue[T](values, key)
}

func requestValue[T any](values *requestValues, key *ValueKey[T]) (T, bool) {
	var zero T
	if values == nil || key == nil {
		return zero, false
	}
	value, ok := values.get(key)
	if !ok {
		return zero, false
	}
	typed, ok := value.(T)
	return typed, ok
}

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

	values *requestValues
}

func newContext(app *App, writer *ResponseWriter, request *http.Request) *Context {
	values := &requestValues{values: make(map[any]any)}
	request = request.WithContext(stdcontext.WithValue(request.Context(), requestValuesContextKey{}, values))
	return &Context{
		app:      app,
		request:  request,
		response: writer,
		logger:   app.config.logger,
		values:   values,
	}
}

// ContextFromRequest returns the Vial context attached to a routed request.
// It lets standard net/http handlers access route metadata without exported
// context keys that could collide with application values.
func ContextFromRequest(request *http.Request) (*Context, bool) {
	if request == nil {
		return nil, false
	}
	contextValue, ok := request.Context().Value(requestContextKey{}).(*Context)
	return contextValue, ok && contextValue != nil
}

// App returns the application serving the request.
func (context *Context) App() *App {
	return context.app
}

// Request returns the underlying HTTP request.
func (context *Context) Request() *http.Request {
	return context.request
}

// Response returns the tracked HTTP response writer.
func (context *Context) Response() http.ResponseWriter {
	return context.response.capabilities
}

// Flush sends buffered response data when the server supports streaming.
func (context *Context) Flush() error {
	return context.ResponseController().Flush()
}

// SetWriteDeadline sets the response write deadline when supported.
func (context *Context) SetWriteDeadline(deadline time.Time) error {
	return context.ResponseController().SetWriteDeadline(deadline)
}

// ResponseController exposes standard-library optional response operations.
func (context *Context) ResponseController() *http.ResponseController {
	return http.NewResponseController(context.Response())
}

// Route returns metadata for the matched route.
func (context *Context) Route() *Route {
	return context.route
}

// Param returns a path parameter by name.
func (context *Context) Param(name string) string {
	return context.request.PathValue(name)
}

// Query returns the first query value for name.
func (context *Context) Query(name string) string {
	return context.request.URL.Query().Get(name)
}

// Header returns a request header value.
func (context *Context) Header(name string) string {
	return context.request.Header.Get(name)
}

// Logger returns the request logger.
func (context *Context) Logger() *slog.Logger {
	if context.logger == nil {
		return slog.Default()
	}
	return context.logger
}

// SetLogger replaces the request logger when logger is non-nil.
func (context *Context) SetLogger(logger *slog.Logger) {
	if logger != nil {
		context.logger = logger
	}
}

// Set stores a request-scoped value.
func (context *Context) Set(key string, value any) {
	context.values.set(stringRequestValueKey(key), value)
}

// Get returns a request-scoped value.
func (context *Context) Get(key string) (any, bool) {
	return context.values.get(stringRequestValueKey(key))
}

// Status returns the response status written so far.
func (context *Context) Status() int {
	return context.response.Status()
}

// BytesWritten returns the response body bytes written so far.
func (context *Context) BytesWritten() int64 {
	return context.response.BytesWritten()
}

// Committed reports whether the response headers were written.
func (context *Context) Committed() bool {
	return context.response.Committed()
}

// JSON writes a JSON response with status.
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

// Text writes a plain-text response with status.
func (context *Context) Text(status int, value string) error {
	if context.Committed() {
		return errors.New("vial: response already committed")
	}

	context.response.Header().Set("Content-Type", "text/plain; charset=utf-8")
	context.response.WriteHeader(status)
	_, err := io.WriteString(context.response, value)
	return err
}

// NoContent writes a response status without a body.
func (context *Context) NoContent(status int) error {
	if context.Committed() {
		return errors.New("vial: response already committed")
	}
	context.response.WriteHeader(status)
	return nil
}

// Redirect writes an HTTP redirect response.
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
	if err := context.bindJSON(destination); err != nil {
		return err
	}
	return validateBinding(destination)
}

func (context *Context) bindJSON(destination any) error {
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
