// Package fault defines transport-neutral application errors.
package fault

import "fmt"

// Kind classifies an application failure without choosing a transport status.
type Kind uint8

const (
	InvalidArgument Kind = iota
	Unauthenticated
	Forbidden
	NotFound
	Conflict
	RateLimited
	Unavailable
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

func (err *Error) Unwrap() error {
	if err == nil {
		return nil
	}
	return err.Cause
}
