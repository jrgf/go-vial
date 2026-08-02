// Package fault defines transport-neutral application errors.
package fault

import "fmt"

// Kind classifies an application failure without choosing a transport status.
type Kind uint8

const (
	// InvalidArgument identifies invalid input supplied by a caller.
	InvalidArgument Kind = iota
	// Unauthenticated identifies a request without valid authentication.
	Unauthenticated
	// Forbidden identifies an authenticated request that is not permitted.
	Forbidden
	// NotFound identifies a requested resource that does not exist.
	NotFound
	// Conflict identifies a request that conflicts with current state.
	Conflict
	// RateLimited identifies a request rejected by a rate limit.
	RateLimited
	// Unavailable identifies a temporarily unavailable dependency or service.
	Unavailable
	// Internal identifies an unexpected internal failure.
	Internal
)

// Error describes an application failure that transports can map independently.
type Error struct {
	Kind    Kind
	Code    string
	Message string
	Fields  map[string]string
	Meta    map[string]any
	Cause   error
}

// New creates an application fault.
func New(kind Kind, code, message string) *Error {
	return &Error{Kind: kind, Code: code, Message: message}
}

// Wrap creates an application fault that retains its cause.
func Wrap(kind Kind, code, message string, cause error) *Error {
	return &Error{Kind: kind, Code: code, Message: message, Cause: cause}
}

// Error returns the fault message and retained cause.
func (err *Error) Error() string {
	if err == nil {
		return "<nil>"
	}
	message := err.Message
	if message == "" {
		message = err.Code
	}
	if message == "" {
		message = "application fault"
	}
	if err.Cause != nil {
		return fmt.Sprintf("%s: %v", message, err.Cause)
	}
	return message
}

// Unwrap returns the retained cause.
func (err *Error) Unwrap() error {
	if err == nil {
		return nil
	}
	return err.Cause
}
