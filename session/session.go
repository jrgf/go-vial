package session

import (
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/jrgf/go-vial"
)

const (
	// MaxCookieBytes is the maximum serialized session cookie size.
	MaxCookieBytes = 4096

	defaultMaxAge       = 24 * time.Hour
	defaultSecureName   = "__Host-vial_session"
	defaultInsecureName = "vial_session"
	formatVersion       = byte(1)
	clockSkewSeconds    = int64(60)
)

var (
	// ErrUnavailable means the session middleware is not installed for a route.
	ErrUnavailable = errors.New("session: middleware is not installed")
	// ErrCommitted means a session was changed after response headers were sent.
	ErrCommitted = errors.New("session: response already committed")
	// ErrTooLarge means the serialized session cannot fit in one cookie.
	ErrTooLarge = errors.New("session: cookie exceeds 4096 bytes")
)

type payload struct {
	IssuedAt int64             `json:"i"`
	Values   map[string]string `json:"v,omitempty"`
	Flashes  []string          `json:"f,omitempty"`
}

// Session is the encrypted request session installed by Manager.Middleware.
// Mutation methods update the pending cookie before returning and must be
// called before the response is committed.
type Session struct {
	mu      sync.RWMutex
	manager *Manager
	context *vial.Context
	data    payload
	pending string
}

// Middleware loads one session and makes it available through Manager.From.
func (manager *Manager) Middleware() vial.Middleware {
	return func(next vial.Handler) vial.Handler {
		return func(context *vial.Context) error {
			current, err := manager.load(context)
			if err != nil {
				return err
			}
			if err := context.BeforeCommit(current.beforeCommit); err != nil {
				return fmt.Errorf("session: register cookie: %w", err)
			}
			manager.valueKey.Set(context, current)
			return next(context)
		}
	}
}

func (manager *Manager) load(context *vial.Context) (*Session, error) {
	current := &Session{manager: manager, context: context, data: newPayload()}
	cookie, err := context.Request().Cookie(manager.name)
	if errors.Is(err, http.ErrNoCookie) {
		return current, nil
	}
	if err != nil {
		return nil, fmt.Errorf("session: read cookie: %w", err)
	}
	decoded, keyIndex, ok := newPayload(), -1, false
	if len(cookie.Value) <= MaxCookieBytes {
		decoded, keyIndex, ok = manager.decode(cookie.Value)
	}
	if !ok {
		current.pending = manager.expiredCookie().String()
		return current, nil
	}
	current.data = decoded
	if keyIndex > 0 {
		current.pending, err = manager.encodedCookie(decoded)
		if err != nil {
			return nil, fmt.Errorf("session: rotate cookie: %w", err)
		}
	}
	return current, nil
}

// From returns the request session installed by Manager.Middleware.
func (manager *Manager) From(context *vial.Context) (*Session, error) {
	if context == nil {
		return nil, ErrUnavailable
	}
	current, ok := manager.valueKey.Get(context)
	if !ok || current == nil {
		return nil, ErrUnavailable
	}
	return current, nil
}

// Get returns one session value.
func (session *Session) Get(key string) (string, bool) {
	if session == nil {
		return "", false
	}
	session.mu.RLock()
	value, ok := session.data.Values[key]
	session.mu.RUnlock()
	return value, ok
}

// Set stores one session value.
func (session *Session) Set(key, value string) error {
	return session.change(func(candidate *payload) bool {
		if current, ok := candidate.Values[key]; ok && current == value {
			return false
		}
		candidate.Values[key] = value
		return true
	})
}

// Delete removes one session value.
func (session *Session) Delete(key string) error {
	return session.change(func(candidate *payload) bool {
		if _, ok := candidate.Values[key]; !ok {
			return false
		}
		delete(candidate.Values, key)
		return true
	})
}

// AddFlash appends a value that Flashes returns once.
func (session *Session) AddFlash(value string) error {
	return session.change(func(candidate *payload) bool {
		candidate.Flashes = append(candidate.Flashes, value)
		return true
	})
}

// Flashes returns and removes all pending flash values.
func (session *Session) Flashes() ([]string, error) {
	var flashes []string
	err := session.change(func(candidate *payload) bool {
		if len(candidate.Flashes) == 0 {
			return false
		}
		flashes = append([]string(nil), candidate.Flashes...)
		candidate.Flashes = nil
		return true
	})
	if err != nil {
		return nil, err
	}
	return flashes, nil
}

// Destroy removes all session data and expires the cookie.
func (session *Session) Destroy() error {
	if err := session.mutable(); err != nil {
		return err
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.context.Committed() {
		return ErrCommitted
	}
	session.data = newPayload()
	session.pending = session.manager.expiredCookie().String()
	return nil
}

func (session *Session) change(change func(*payload) bool) error {
	if err := session.mutable(); err != nil {
		return err
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.context.Committed() {
		return ErrCommitted
	}
	candidate := clonePayload(session.data)
	if !change(&candidate) {
		return nil
	}
	if candidate.IssuedAt == 0 {
		candidate.IssuedAt = session.manager.now().Unix()
	}
	pending, err := session.manager.encodedCookie(candidate)
	if err != nil {
		return err
	}
	session.data = candidate
	session.pending = pending
	return nil
}

func (session *Session) mutable() error {
	if session == nil || session.manager == nil || session.context == nil {
		return ErrUnavailable
	}
	if session.context.Committed() {
		return ErrCommitted
	}
	return nil
}

func (session *Session) beforeCommit(header http.Header) {
	session.mu.RLock()
	pending := session.pending
	session.mu.RUnlock()
	if pending != "" {
		header.Add("Set-Cookie", pending)
	}
}

func newPayload() payload {
	return payload{Values: make(map[string]string)}
}

func clonePayload(source payload) payload {
	clone := newPayload()
	clone.IssuedAt = source.IssuedAt
	for key, value := range source.Values {
		clone.Values[key] = value
	}
	clone.Flashes = append([]string(nil), source.Flashes...)
	return clone
}
