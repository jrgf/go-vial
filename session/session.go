package session

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
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

// Config controls cookie security, scope, lifetime, and encryption keys.
// Keys are ordered newest first; each key must contain at least 32 random bytes.
type Config struct {
	Keys [][]byte

	Name     string
	Path     string
	Domain   string
	MaxAge   time.Duration
	SameSite http.SameSite

	// DangerouslyAllowInsecureCookies disables the Secure attribute for local
	// HTTP development. Production applications should leave it false.
	DangerouslyAllowInsecureCookies bool
}

// Manager loads and persists request sessions.
type Manager struct {
	mu   sync.RWMutex
	keys [][]byte

	name     string
	path     string
	domain   string
	maxAge   time.Duration
	sameSite http.SameSite
	secure   bool
	valueKey *vial.ValueKey[*Session]
	now      func() time.Time
}

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

// New validates config and creates a session manager.
func New(config Config) (*Manager, error) {
	secure := !config.DangerouslyAllowInsecureCookies
	name := strings.TrimSpace(config.Name)
	if name == "" {
		if secure {
			name = defaultSecureName
		} else {
			name = defaultInsecureName
		}
	}
	path := config.Path
	if path == "" {
		path = "/"
	}
	if !strings.HasPrefix(path, "/") {
		return nil, errors.New("session: cookie path must start with /")
	}
	maxAge := config.MaxAge
	if maxAge == 0 {
		maxAge = defaultMaxAge
	}
	seconds := int64(maxAge / time.Second)
	if maxAge < time.Second || maxAge%time.Second != 0 || seconds > int64(^uint(0)>>1) {
		return nil, errors.New("session: MaxAge must fit a positive whole number of seconds")
	}
	sameSite := config.SameSite
	if sameSite == 0 {
		sameSite = http.SameSiteLaxMode
	}
	switch sameSite {
	case http.SameSiteLaxMode, http.SameSiteStrictMode, http.SameSiteNoneMode:
	default:
		return nil, errors.New("session: SameSite must be Lax, Strict, or None")
	}
	if sameSite == http.SameSiteNoneMode && !secure {
		return nil, errors.New("session: SameSite=None requires secure cookies")
	}
	if strings.HasPrefix(name, "__Host-") && (!secure || path != "/" || config.Domain != "") {
		return nil, errors.New("session: __Host- cookies require Secure, Path=/, and no Domain")
	}
	if strings.HasPrefix(name, "__Secure-") && !secure {
		return nil, errors.New("session: __Secure- cookies require Secure")
	}

	manager := &Manager{
		name:     name,
		path:     path,
		domain:   config.Domain,
		maxAge:   maxAge,
		sameSite: sameSite,
		secure:   secure,
		valueKey: vial.NewValueKey[*Session]("session"),
		now:      time.Now,
	}
	if err := manager.cookie("probe", int(seconds), time.Now().Add(maxAge)).Valid(); err != nil {
		return nil, fmt.Errorf("session: invalid cookie configuration: %w", err)
	}
	if err := manager.ReplaceKeys(config.Keys...); err != nil {
		return nil, err
	}
	return manager, nil
}

// CookieName returns the configured session cookie name.
func (manager *Manager) CookieName() string {
	return manager.name
}

// ReplaceKeys atomically replaces the encryption keys. New cookies use the
// first key and existing cookies may use any listed key.
func (manager *Manager) ReplaceKeys(keys ...[]byte) error {
	if len(keys) == 0 {
		return errors.New("session: at least one key is required")
	}
	derived := make([][]byte, 0, len(keys))
	for index, key := range keys {
		if len(key) < 32 {
			return fmt.Errorf("session: key %d must contain at least 32 bytes", index)
		}
		derived = append(derived, deriveKey(key))
	}
	manager.mu.Lock()
	manager.keys = derived
	manager.mu.Unlock()
	return nil
}

// Middleware loads one session and makes it available through Manager.From.
func (manager *Manager) Middleware() vial.Middleware {
	return func(next vial.Handler) vial.Handler {
		return func(context *vial.Context) error {
			current := &Session{
				manager: manager,
				context: context,
				data:    newPayload(),
			}
			cookie, err := context.Request().Cookie(manager.name)
			switch {
			case errors.Is(err, http.ErrNoCookie):
			case err != nil:
				return fmt.Errorf("session: read cookie: %w", err)
			default:
				decoded, keyIndex, ok := newPayload(), -1, false
				if len(cookie.Value) <= MaxCookieBytes {
					decoded, keyIndex, ok = manager.decode(cookie.Value)
				}
				if !ok {
					current.pending = manager.expiredCookie().String()
				} else {
					current.data = decoded
					if keyIndex > 0 {
						current.pending, err = manager.encodedCookie(decoded)
						if err != nil {
							return err
						}
					}
				}
			}
			if err := context.BeforeCommit(current.beforeCommit); err != nil {
				return err
			}
			manager.valueKey.Set(context, current)
			initialPending := current.pending
			handlerErr := next(context)
			if handlerErr != nil && !context.Committed() {
				current.mu.Lock()
				current.pending = initialPending
				current.mu.Unlock()
			}
			return handlerErr
		}
	}
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

func (manager *Manager) decode(value string) (payload, int, bool) {
	encoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || len(encoded) == 0 || encoded[0] != formatVersion {
		return newPayload(), -1, false
	}
	for index, key := range manager.currentKeys() {
		aead, err := newAEAD(key)
		if err != nil || len(encoded) < 1+aead.NonceSize()+aead.Overhead() {
			continue
		}
		nonce := encoded[1 : 1+aead.NonceSize()]
		ciphertext := encoded[1+aead.NonceSize():]
		plaintext, err := aead.Open(nil, nonce, ciphertext, []byte(manager.name))
		if err != nil {
			continue
		}
		decoded := newPayload()
		if err := json.Unmarshal(plaintext, &decoded); err != nil {
			continue
		}
		now := manager.now().Unix()
		if decoded.IssuedAt <= 0 || decoded.IssuedAt > now+clockSkewSeconds || decoded.IssuedAt < now-int64(manager.maxAge/time.Second) {
			continue
		}
		if decoded.Values == nil {
			decoded.Values = make(map[string]string)
		}
		return decoded, index, true
	}
	return newPayload(), -1, false
}

func (manager *Manager) encodedCookie(data payload) (string, error) {
	if len(data.Values) == 0 && len(data.Flashes) == 0 {
		return manager.expiredCookie().String(), nil
	}
	now := manager.now()
	data.IssuedAt = now.Unix()
	plaintext, err := json.Marshal(data)
	if err != nil {
		return "", fmt.Errorf("session: encode cookie: %w", err)
	}
	keys := manager.currentKeys()
	aead, err := newAEAD(keys[0])
	if err != nil {
		return "", fmt.Errorf("session: initialize encryption: %w", err)
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("session: generate nonce: %w", err)
	}
	encoded := make([]byte, 1, 1+len(nonce)+len(plaintext)+aead.Overhead())
	encoded[0] = formatVersion
	encoded = append(encoded, nonce...)
	encoded = aead.Seal(encoded, nonce, plaintext, []byte(manager.name))
	value := base64.RawURLEncoding.EncodeToString(encoded)
	cookie := manager.cookie(
		value,
		int(manager.maxAge/time.Second),
		now.Add(manager.maxAge),
	)
	serialized := cookie.String()
	if serialized == "" {
		return "", errors.New("session: invalid cookie")
	}
	if len(serialized) > MaxCookieBytes {
		return "", ErrTooLarge
	}
	return serialized, nil
}

func (manager *Manager) expiredCookie() *http.Cookie {
	return manager.cookie("", -1, time.Unix(1, 0))
}

func (manager *Manager) cookie(value string, maxAge int, expires time.Time) *http.Cookie {
	return &http.Cookie{
		Name:     manager.name,
		Value:    value,
		Path:     manager.path,
		Domain:   manager.domain,
		Expires:  expires,
		MaxAge:   maxAge,
		Secure:   manager.secure,
		HttpOnly: true,
		SameSite: manager.sameSite,
	}
}

func (manager *Manager) currentKeys() [][]byte {
	manager.mu.RLock()
	keys := append([][]byte(nil), manager.keys...)
	manager.mu.RUnlock()
	return keys
}

func deriveKey(secret []byte) []byte {
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte("go-vial/session/v1/encryption"))
	return mac.Sum(nil)
}

func newAEAD(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
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
