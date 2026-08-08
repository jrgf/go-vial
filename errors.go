package vial

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/jrgf/go-vial/fault"
)

// HTTPError is an application error with a stable HTTP representation.
type HTTPError struct {
	Status  int
	Code    string
	Message string
	Cause   error
	Headers http.Header
}

// Error returns the public error message and retained cause.
func (err *HTTPError) Error() string {
	if err == nil {
		return "<nil>"
	}
	if err.Cause != nil {
		return fmt.Sprintf("%s: %v", err.Message, err.Cause)
	}
	return err.Message
}

// Unwrap returns the retained cause.
func (err *HTTPError) Unwrap() error {
	if err == nil {
		return nil
	}
	return err.Cause
}

// ErrorHandler renders errors returned by handlers or middleware.
type ErrorHandler func(*Context, error)

func renderErrorSafely(context *Context, requestErr error, handler ErrorHandler) {
	defer func() {
		if recovered := recover(); recovered != nil {
			context.Logger().Error(
				"custom error handler panicked",
				"request_error", requestErr,
				"panic", recovered,
			)
			if context.Committed() {
				return
			}
			for key := range context.response.Header() {
				context.response.Header().Del(key)
			}
			context.response.Header().Set("Content-Type", "text/plain; charset=utf-8")
			context.response.Header().Set("X-Content-Type-Options", "nosniff")
			context.response.WriteHeader(http.StatusInternalServerError)
			_, _ = context.response.Write([]byte("Internal Server Error\n"))
		}
	}()
	handler(context, requestErr)
}

// NewHTTPError creates an HTTP error with a stable status, code, and message.
func NewHTTPError(status int, code, message string) *HTTPError {
	return &HTTPError{Status: status, Code: code, Message: message}
}

// WrapHTTPError creates an HTTP error that retains cause.
func WrapHTTPError(status int, code, message string, cause error) *HTTPError {
	return &HTTPError{Status: status, Code: code, Message: message, Cause: cause}
}

// BadRequest creates a 400 HTTP error.
func BadRequest(code, message string) *HTTPError {
	return NewHTTPError(http.StatusBadRequest, code, message)
}

// Unauthorized creates a 401 HTTP error.
func Unauthorized(code, message string) *HTTPError {
	return NewHTTPError(http.StatusUnauthorized, code, message)
}

// Forbidden creates a 403 HTTP error.
func Forbidden(code, message string) *HTTPError {
	return NewHTTPError(http.StatusForbidden, code, message)
}

// NotFound creates a 404 HTTP error.
func NotFound(code, message string) *HTTPError {
	return NewHTTPError(http.StatusNotFound, code, message)
}

// MethodNotAllowed creates a 405 HTTP error.
func MethodNotAllowed(code, message string) *HTTPError {
	return NewHTTPError(http.StatusMethodNotAllowed, code, message)
}

// Conflict creates a 409 HTTP error.
func Conflict(code, message string) *HTTPError {
	return NewHTTPError(http.StatusConflict, code, message)
}

// UnsupportedMediaType creates a 415 HTTP error.
func UnsupportedMediaType(code, message string) *HTTPError {
	return NewHTTPError(http.StatusUnsupportedMediaType, code, message)
}

// RequestEntityTooLarge creates a 413 HTTP error.
func RequestEntityTooLarge(code, message string) *HTTPError {
	return NewHTTPError(http.StatusRequestEntityTooLarge, code, message)
}

// InternalServerError creates a generic 500 HTTP error that retains cause.
func InternalServerError(cause error) *HTTPError {
	return WrapHTTPError(
		http.StatusInternalServerError,
		"internal_server_error",
		"An unexpected error occurred",
		cause,
	)
}

// StatusCode returns the HTTP status represented by err, defaulting to 500.
func StatusCode(err error) int {
	return mapHTTPError(err).status
}

func applyHTTPErrorHeaders(context *Context, err error) {
	if errors.Is(err, ErrAsyncQueueFull) || errors.Is(err, ErrAsyncUnavailable) {
		context.response.Header().Set("Retry-After", "5")
	}
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) || httpErr == nil {
		return
	}
	for key, values := range httpErr.Headers {
		context.response.Header()[key] = append([]string(nil), values...)
	}
}

func defaultErrorHandler(context *Context, err error) {
	if context.Committed() {
		context.Logger().Error("handler failed after response was committed", "error", err)
		return
	}

	mapped := mapHTTPError(err)

	if mapped.status >= http.StatusInternalServerError {
		context.Logger().Error("request failed", "error", err)
	}

	context.response.Header().Set("Content-Type", "application/json; charset=utf-8")
	context.response.WriteHeader(mapped.status)
	details := map[string]any{
		"code":    mapped.code,
		"message": mapped.message,
	}
	if len(mapped.fields) > 0 {
		details["fields"] = mapped.fields
	}
	_ = json.NewEncoder(context.response).Encode(map[string]any{
		"error": details,
	})
}

type mappedHTTPError struct {
	status  int
	code    string
	message string
	fields  map[string]string
}

func mapHTTPError(err error) mappedHTTPError {
	mapped := mappedHTTPError{
		status:  http.StatusInternalServerError,
		code:    "internal_server_error",
		message: "An unexpected error occurred",
	}

	var httpErr *HTTPError
	if errors.As(err, &httpErr) && httpErr != nil {
		if httpErr.Status >= 400 && httpErr.Status <= 599 {
			mapped.status = httpErr.Status
		}
		if httpErr.Code != "" {
			mapped.code = httpErr.Code
		}
		if httpErr.Message != "" {
			mapped.message = httpErr.Message
		}
		return mapped
	}
	var operationErr *OperationError
	if errors.As(err, &operationErr) && operationErr != nil {
		mapped.code = operationErr.Code
		mapped.message = operationErr.Message
		if mapped.code == "" {
			mapped.code = "operation_failed"
		}
		if mapped.message == "" {
			mapped.message = "The operation could not be completed"
		}
		return mapped
	}

	switch {
	case errors.Is(err, ErrAsyncQueueFull):
		return mappedHTTPError{status: http.StatusServiceUnavailable, code: "async_queue_full", message: "The operation could not be accepted at this time"}
	case errors.Is(err, ErrAsyncUnavailable):
		return mappedHTTPError{status: http.StatusServiceUnavailable, code: "async_unavailable", message: "Asynchronous operations are unavailable"}
	case errors.Is(err, ErrInvalidOperation):
		return mappedHTTPError{status: http.StatusBadRequest, code: "invalid_async_operation", message: "The asynchronous operation is invalid"}
	case errors.Is(err, ErrOperationNotFound):
		return mappedHTTPError{status: http.StatusNotFound, code: "operation_not_found", message: "The operation was not found"}
	case errors.Is(err, ErrOperationFinished):
		return mappedHTTPError{status: http.StatusConflict, code: "operation_finished", message: "The operation is already finished"}
	case errors.Is(err, ErrRetriesUnsupported):
		return mappedHTTPError{status: http.StatusBadRequest, code: "async_retries_unsupported", message: "The executor does not support durable retries"}
	case errors.Is(err, ErrInvalidPreference):
		return mappedHTTPError{status: http.StatusBadRequest, code: "invalid_prefer", message: "The Prefer header is invalid"}
	}

	var faultErr *fault.Error
	if !errors.As(err, &faultErr) || faultErr == nil {
		return mapped
	}
	mapped.status, mapped.code = faultHTTPStatus(faultErr.Kind)
	if faultErr.Code != "" {
		mapped.code = faultErr.Code
	}
	if mapped.status < http.StatusInternalServerError {
		if faultErr.Message != "" {
			mapped.message = faultErr.Message
		} else {
			mapped.message = http.StatusText(mapped.status)
		}
		mapped.fields = faultErr.Fields
	}
	return mapped
}

func faultHTTPStatus(kind fault.Kind) (int, string) {
	switch kind {
	case fault.InvalidArgument:
		return http.StatusBadRequest, "invalid_argument"
	case fault.Unauthenticated:
		return http.StatusUnauthorized, "unauthenticated"
	case fault.Forbidden:
		return http.StatusForbidden, "forbidden"
	case fault.NotFound:
		return http.StatusNotFound, "not_found"
	case fault.Conflict:
		return http.StatusConflict, "conflict"
	case fault.RateLimited:
		return http.StatusTooManyRequests, "rate_limited"
	case fault.Unavailable:
		return http.StatusServiceUnavailable, "unavailable"
	case fault.Internal:
		return http.StatusInternalServerError, "internal_server_error"
	default:
		return http.StatusInternalServerError, "internal_server_error"
	}
}
