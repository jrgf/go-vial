package vial

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
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
	var httpErr *HTTPError
	if errors.As(err, &httpErr) && httpErr.Status >= 400 && httpErr.Status <= 599 {
		return httpErr.Status
	}
	return http.StatusInternalServerError
}

func applyHTTPErrorHeaders(context *Context, err error) {
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) {
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

	status := http.StatusInternalServerError
	code := "internal_server_error"
	message := "An unexpected error occurred"

	var httpErr *HTTPError
	if errors.As(err, &httpErr) {
		if httpErr.Status >= 400 && httpErr.Status <= 599 {
			status = httpErr.Status
		}
		if httpErr.Code != "" {
			code = httpErr.Code
		}
		if httpErr.Message != "" {
			message = httpErr.Message
		}
	}

	if status >= http.StatusInternalServerError {
		context.Logger().Error("request failed", "error", err)
	}

	context.response.Header().Set("Content-Type", "application/json; charset=utf-8")
	context.response.WriteHeader(status)
	_ = json.NewEncoder(context.response).Encode(map[string]any{
		"error": map[string]string{
			"code":    code,
			"message": message,
		},
	})
}
