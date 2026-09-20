package session

import (
	"crypto/cipher"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/jrgf/go-vial"
)

// Config controls cookie security, scope, lifetime, and encryption keys.
// Keys are ordered newest first; each key must contain at least 32 random bytes.
type Config struct {
	Keys [][]byte

	Name     string
	Path     string
	Domain   string
	MaxAge   time.Duration // Absolute lifetime; mutations and key rotation do not extend it.
	SameSite http.SameSite

	// DangerouslyAllowInsecureCookies disables the Secure attribute for local
	// HTTP development. Production applications should leave it false.
	DangerouslyAllowInsecureCookies bool
}

// Manager loads and persists request sessions.
type Manager struct {
	mu   sync.RWMutex
	keys []cipher.AEAD

	name     string
	path     string
	domain   string
	maxAge   time.Duration
	sameSite http.SameSite
	secure   bool
	valueKey *vial.ValueKey[*Session]
	now      func() time.Time
}

// New validates config and creates a session manager.
func New(config Config) (*Manager, error) {
	manager := &Manager{name: strings.TrimSpace(config.Name), path: config.Path, domain: config.Domain, maxAge: config.MaxAge, sameSite: config.SameSite, secure: !config.DangerouslyAllowInsecureCookies, valueKey: vial.NewValueKey[*Session]("session"), now: time.Now}
	manager.defaults()
	if err := manager.validate(); err != nil {
		return nil, err
	}
	if err := manager.ReplaceKeys(config.Keys...); err != nil {
		return nil, err
	}
	return manager, nil
}

func (manager *Manager) defaults() {
	if manager.name == "" {
		manager.name = defaultInsecureName
		if manager.secure {
			manager.name = defaultSecureName
		}
	}
	if manager.path == "" {
		manager.path = "/"
	}
	if manager.maxAge == 0 {
		manager.maxAge = defaultMaxAge
	}
	if manager.sameSite == 0 {
		manager.sameSite = http.SameSiteLaxMode
	}
}

func (manager *Manager) validate() error {
	if !strings.HasPrefix(manager.path, "/") {
		return errors.New("session: cookie path must start with /")
	}
	seconds := int64(manager.maxAge / time.Second)
	if manager.maxAge < time.Second || manager.maxAge%time.Second != 0 || seconds > int64(^uint(0)>>1) {
		return errors.New("session: MaxAge must fit a positive whole number of seconds")
	}
	if err := manager.validateSecurity(); err != nil {
		return err
	}
	if err := manager.cookie("probe", int(seconds), manager.now().Add(manager.maxAge)).Valid(); err != nil {
		return fmt.Errorf("session: invalid cookie configuration: %w", err)
	}
	return nil
}

func (manager *Manager) validateSecurity() error {
	switch manager.sameSite {
	case http.SameSiteLaxMode, http.SameSiteStrictMode, http.SameSiteNoneMode:
	default:
		return errors.New("session: SameSite must be Lax, Strict, or None")
	}
	if manager.sameSite == http.SameSiteNoneMode && !manager.secure {
		return errors.New("session: SameSite=None requires secure cookies")
	}
	if strings.HasPrefix(manager.name, "__Host-") && (!manager.secure || manager.path != "/" || manager.domain != "") {
		return errors.New("session: __Host- cookies require Secure, Path=/, and no Domain")
	}
	if strings.HasPrefix(manager.name, "__Secure-") && !manager.secure {
		return errors.New("session: __Secure- cookies require Secure")
	}
	return nil
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
	derived := make([]cipher.AEAD, 0, len(keys))
	for index, key := range keys {
		if len(key) < 32 {
			return fmt.Errorf("session: key %d must contain at least 32 bytes", index)
		}
		aead, err := newAEAD(deriveKey(key))
		if err != nil {
			return fmt.Errorf("session: initialize key %d: %w", index, err)
		}
		derived = append(derived, aead)
	}
	manager.mu.Lock()
	manager.keys = derived
	manager.mu.Unlock()
	return nil
}
