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

func (err *HTTPError) Error() string {
	if err == nil {
		return "<nil>"
	}
	if err.Cause != nil {
		return fmt.Sprintf("%s: %v", err.Message, err.Cause)
	}
	return err.Message
}

func (err *HTTPError) Unwrap() error {
	if err == nil {
		return nil
	}
	return err.Cause
}

// ErrorHandler renders errors returned by handlers or middleware.
type ErrorHandler func(*Context, error)

func NewHTTPError(status int, code, message string) *HTTPError {
	return &HTTPError{Status: status, Code: code, Message: message}
}

func WrapHTTPError(status int, code, message string, cause error) *HTTPError {
	return &HTTPError{Status: status, Code: code, Message: message, Cause: cause}
}

func BadRequest(code, message string) *HTTPError {
	return NewHTTPError(http.StatusBadRequest, code, message)
}

func Unauthorized(code, message string) *HTTPError {
	return NewHTTPError(http.StatusUnauthorized, code, message)
}

func Forbidden(code, message string) *HTTPError {
	return NewHTTPError(http.StatusForbidden, code, message)
}

func NotFound(code, message string) *HTTPError {
	return NewHTTPError(http.StatusNotFound, code, message)
}

func MethodNotAllowed(code, message string) *HTTPError {
	return NewHTTPError(http.StatusMethodNotAllowed, code, message)
}

func Conflict(code, message string) *HTTPError {
	return NewHTTPError(http.StatusConflict, code, message)
}

func UnsupportedMediaType(code, message string) *HTTPError {
	return NewHTTPError(http.StatusUnsupportedMediaType, code, message)
}

func RequestEntityTooLarge(code, message string) *HTTPError {
	return NewHTTPError(http.StatusRequestEntityTooLarge, code, message)
}

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
