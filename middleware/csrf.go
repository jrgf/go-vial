package middleware

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/jrgf/go-vial"
)

const (
	csrfContextKey    = "vial.middleware.csrf.token"
	csrfCookieName    = "__Host-vial_csrf"
	csrfDevCookieName = "vial_csrf"
	csrfHeaderName    = "X-CSRF-Token"
	csrfFieldName     = "_csrf"
	csrfTokenBytes    = 32
	csrfMaxAge        = 12 * time.Hour
)

// CSRFConfig defines the signing key and accepted browser origins.
type CSRFConfig struct {
	Key []byte
	// TrustedOrigins overrides request-host matching, typically behind a proxy.
	TrustedOrigins []string
	// AllowInsecure permits the CSRF cookie over local HTTP only.
	AllowInsecure bool
}

type csrfPolicy struct {
	key            []byte
	trustedOrigins map[string]struct{}
	secure         bool
	cookieName     string
}

// CSRF protects state-changing requests with origin verification and a signed
// double-submit token. Form posts use _csrf; other requests may use
// X-CSRF-Token.
func CSRF(config CSRFConfig) (vial.Middleware, error) {
	policy, err := newCSRFPolicy(config)
	if err != nil {
		return nil, err
	}

	return func(next vial.Handler) vial.Handler {
		return func(context *vial.Context) error {
			request := context.Request()
			addVary(context.Response().Header(), "Cookie")
			if csrfSafeMethod(request.Method) {
				token, err := policy.issueToken(context)
				if err != nil {
					return err
				}
				context.Set(csrfContextKey, token)
				return next(context)
			}

			if !policy.allowsRequestOrigin(request) {
				return csrfForbidden()
			}
			cookie, err := request.Cookie(policy.cookieName)
			if err != nil || !policy.validToken(cookie.Value) {
				return csrfForbidden()
			}
			token, err := csrfRequestToken(context)
			if err != nil {
				return err
			}
			if token == "" || !hmac.Equal([]byte(token), []byte(cookie.Value)) {
				return csrfForbidden()
			}
			context.Set(csrfContextKey, token)
			return next(context)
		}
	}, nil
}

// CSRFToken returns the request token exposed by CSRF middleware.
func CSRFToken(context *vial.Context) string {
	value, ok := context.Get(csrfContextKey)
	if !ok {
		return ""
	}
	token, _ := value.(string)
	return token
}

func newCSRFPolicy(config CSRFConfig) (csrfPolicy, error) {
	if len(config.Key) < 32 {
		return csrfPolicy{}, fmt.Errorf("csrf key must be at least 32 bytes")
	}
	origins := make(map[string]struct{}, len(config.TrustedOrigins))
	for _, value := range config.TrustedOrigins {
		origin, err := normalizeOrigin(value, false)
		if err != nil {
			return csrfPolicy{}, fmt.Errorf("csrf trusted origin %q: %w", value, err)
		}
		origins[origin] = struct{}{}
	}
	secure := !config.AllowInsecure
	cookieName := csrfCookieName
	if !secure {
		cookieName = csrfDevCookieName
	}
	return csrfPolicy{
		key:            append([]byte(nil), config.Key...),
		trustedOrigins: origins,
		secure:         secure,
		cookieName:     cookieName,
	}, nil
}

func (policy csrfPolicy) issueToken(context *vial.Context) (string, error) {
	if cookie, err := context.Request().Cookie(policy.cookieName); err == nil && policy.validToken(cookie.Value) {
		return cookie.Value, nil
	}

	randomValue := make([]byte, csrfTokenBytes)
	if _, err := rand.Read(randomValue); err != nil {
		return "", fmt.Errorf("generate csrf token: %w", err)
	}
	token := policy.sign(randomValue)
	http.SetCookie(context.Response(), &http.Cookie{
		Name:     policy.cookieName,
		Value:    token,
		Path:     "/",
		Expires:  time.Now().Add(csrfMaxAge),
		MaxAge:   int(csrfMaxAge / time.Second),
		Secure:   policy.secure,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})
	return token, nil
}

func (policy csrfPolicy) sign(randomValue []byte) string {
	mac := hmac.New(sha256.New, policy.key)
	_, _ = mac.Write(randomValue)
	return base64.RawURLEncoding.EncodeToString(randomValue) + "." +
		base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (policy csrfPolicy) validToken(token string) bool {
	randomPart, signaturePart, ok := strings.Cut(token, ".")
	if !ok {
		return false
	}
	randomValue, err := base64.RawURLEncoding.DecodeString(randomPart)
	if err != nil || len(randomValue) != csrfTokenBytes {
		return false
	}
	signature, err := base64.RawURLEncoding.DecodeString(signaturePart)
	if err != nil || len(signature) != sha256.Size {
		return false
	}
	mac := hmac.New(sha256.New, policy.key)
	_, _ = mac.Write(randomValue)
	return hmac.Equal(signature, mac.Sum(nil))
}

func (policy csrfPolicy) allowsRequestOrigin(request *http.Request) bool {
	value := request.Header.Get("Origin")
	allowPath := false
	if value == "" {
		value = request.Referer()
		allowPath = true
	}
	if value == "" {
		return false
	}
	source, err := normalizeOrigin(value, allowPath)
	if err != nil {
		return false
	}
	if len(policy.trustedOrigins) > 0 {
		_, ok := policy.trustedOrigins[source]
		return ok
	}

	scheme := "http"
	if request.TLS != nil {
		scheme = "https"
	}
	target, err := normalizeOrigin(scheme+"://"+request.Host, false)
	return err == nil && source == target
}

func normalizeOrigin(value string, allowPath bool) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Host == "" || parsed.User != nil {
		return "", errors.New("must be an absolute HTTP origin")
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", errors.New("must use HTTP or HTTPS")
	}
	if !allowPath && ((parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.Fragment != "") {
		return "", errors.New("must not contain a path, query, or fragment")
	}
	hostname := strings.ToLower(parsed.Hostname())
	if hostname == "" {
		return "", errors.New("must include a host")
	}
	if strings.Contains(hostname, ":") && net.ParseIP(hostname) == nil {
		return "", errors.New("must include a valid host")
	}
	port := parsed.Port()
	if (scheme == "http" && port == "80") || (scheme == "https" && port == "443") {
		port = ""
	}
	host := hostname
	if port != "" {
		host = net.JoinHostPort(hostname, port)
	} else if strings.Contains(hostname, ":") {
		host = "[" + hostname + "]"
	}
	return scheme + "://" + host, nil
}

func csrfRequestToken(context *vial.Context) (string, error) {
	if token := strings.TrimSpace(context.Header(csrfHeaderName)); token != "" {
		return token, nil
	}
	mediaType, _, err := mime.ParseMediaType(context.Request().Header.Get("Content-Type"))
	if err != nil || (mediaType != "application/x-www-form-urlencoded" && mediaType != "multipart/form-data") {
		return "", nil
	}
	return context.FormValue(csrfFieldName)
}

func csrfSafeMethod(method string) bool {
	return method == http.MethodGet || method == http.MethodHead || method == http.MethodOptions
}

func csrfForbidden() error {
	return vial.Forbidden("csrf_forbidden", "CSRF validation failed")
}
