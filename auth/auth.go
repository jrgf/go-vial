package auth

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/jrgf/go-vial"
)

var (
	// ErrInvalidCredentials tells Manager.Middleware to return a safe 401
	// response without exposing credential-validation details.
	ErrInvalidCredentials = errors.New("auth: invalid credentials")
	// ErrUnavailable means a guard ran without Manager.Middleware.
	ErrUnavailable = errors.New("auth: middleware is not installed")
)

// Identity describes one authenticated caller. Subject is the stable caller
// identifier. Grants are application-defined roles, permissions, or scopes.
type Identity struct {
	Subject string
	Grants  []string
}

// Has reports whether the identity contains a grant.
func (identity Identity) Has(grant string) bool {
	for _, current := range identity.Grants {
		if current == grant {
			return true
		}
	}
	return false
}

// Authenticator resolves optional credentials. Authenticated must be true only
// when Identity represents a verified caller. Missing credentials return the
// zero identity, false, nil.
type Authenticator func(*vial.Context) (identity Identity, authenticated bool, err error)

// Config defines identity resolution and the HTTP authentication challenge.
type Config struct {
	Authenticate Authenticator
	Challenge    string
}

// Manager installs request identities and creates authorization guards.
type Manager struct {
	authenticate Authenticator
	challenge    string
	valueKey     *vial.ValueKey[requestState]
}

type requestState struct {
	identity      Identity
	authenticated bool
}

// New validates config and creates an authentication manager.
func New(config Config) (*Manager, error) {
	if config.Authenticate == nil {
		return nil, errors.New("auth: authenticator is required")
	}
	if !validChallenge(config.Challenge) {
		return nil, errors.New("auth: Challenge must be a non-empty HTTP header value")
	}
	return &Manager{
		authenticate: config.Authenticate,
		challenge:    config.Challenge,
		valueKey:     vial.NewValueKey[requestState]("auth_identity"),
	}, nil
}

// Middleware resolves optional credentials and installs the request identity.
// Invalid credentials stop the request with a challenged 401 response.
func (manager *Manager) Middleware() vial.Middleware {
	return func(next vial.Handler) vial.Handler {
		return func(context *vial.Context) error {
			identity, authenticated, err := manager.authenticate(context)
			if errors.Is(err, ErrInvalidCredentials) {
				return manager.unauthorized("invalid_credentials", "Credentials are invalid")
			}
			if err != nil {
				return fmt.Errorf("auth: authenticate request: %w", err)
			}
			if !authenticated {
				if identity.Subject != "" || len(identity.Grants) != 0 {
					return errors.New("auth: authenticator returned an identity without authentication")
				}
				manager.valueKey.Set(context, requestState{})
				return next(context)
			}
			normalized, err := normalizeIdentity(identity)
			if err != nil {
				return err
			}
			manager.valueKey.Set(context, requestState{identity: normalized, authenticated: true})
			return next(context)
		}
	}
}

// From returns a copy of the authenticated request identity.
func (manager *Manager) From(context *vial.Context) (Identity, bool) {
	if context == nil {
		return Identity{}, false
	}
	state, ok := manager.valueKey.Get(context)
	if !ok || !state.authenticated {
		return Identity{}, false
	}
	return cloneIdentity(state.identity), true
}

// Require returns middleware that requires authentication and every listed
// grant. With no grants it requires only an authenticated identity.
func (manager *Manager) Require(grants ...string) vial.Middleware {
	required := append([]string(nil), grants...)
	return func(next vial.Handler) vial.Handler {
		return func(context *vial.Context) error {
			state, ok := manager.valueKey.Get(context)
			if !ok {
				return ErrUnavailable
			}
			if !state.authenticated {
				return manager.unauthorized("authentication_required", "Authentication is required")
			}
			for _, grant := range required {
				if !state.identity.Has(grant) {
					return vial.Forbidden("insufficient_grant", "The authenticated identity is not permitted")
				}
			}
			return next(context)
		}
	}
}

func (manager *Manager) unauthorized(code, message string) *vial.HTTPError {
	err := vial.Unauthorized(code, message)
	err.Headers = make(http.Header)
	err.Headers.Set("WWW-Authenticate", manager.challenge)
	return err
}

func normalizeIdentity(identity Identity) (Identity, error) {
	if identity.Subject == "" || strings.TrimSpace(identity.Subject) != identity.Subject {
		return Identity{}, errors.New("auth: authenticated identity requires a non-empty trimmed subject")
	}
	normalized := Identity{Subject: identity.Subject, Grants: make([]string, 0, len(identity.Grants))}
	seen := make(map[string]struct{}, len(identity.Grants))
	for _, grant := range identity.Grants {
		if grant == "" || strings.TrimSpace(grant) != grant {
			return Identity{}, errors.New("auth: identity grants must be non-empty and trimmed")
		}
		if _, exists := seen[grant]; exists {
			continue
		}
		seen[grant] = struct{}{}
		normalized.Grants = append(normalized.Grants, grant)
	}
	return normalized, nil
}

func cloneIdentity(identity Identity) Identity {
	identity.Grants = append([]string(nil), identity.Grants...)
	return identity
}

func validChallenge(value string) bool {
	if value == "" || strings.TrimSpace(value) != value {
		return false
	}
	schemeEnd := len(value)
	for index := 0; index < len(value); index++ {
		character := value[index]
		if character == '\r' || character == '\n' || character == 0x7f || (character < 0x20 && character != '\t') {
			return false
		}
		if (character == ' ' || character == '\t' || character == ',') && schemeEnd == len(value) {
			schemeEnd = index
		}
	}
	if schemeEnd == 0 {
		return false
	}
	for index := 0; index < schemeEnd; index++ {
		character := value[index]
		if !isTokenCharacter(character) {
			return false
		}
	}
	return true
}

func isTokenCharacter(character byte) bool {
	if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' {
		return true
	}
	return strings.ContainsRune("!#$%&'*+-.^_`|~", rune(character))
}
