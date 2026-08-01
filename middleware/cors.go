package middleware

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jrgf/go-vial"
)

// CORSConfig defines an explicit browser cross-origin policy.
type CORSConfig struct {
	AllowedOrigins   []string
	AllowedMethods   []string
	AllowedHeaders   []string
	ExposedHeaders   []string
	AllowCredentials bool
	MaxAge           time.Duration
}

type corsPolicy struct {
	origins          map[string]struct{}
	methods          map[string]struct{}
	headers          map[string]struct{}
	methodValues     []string
	headerValues     []string
	exposedValues    []string
	wildcardOrigin   bool
	allowCredentials bool
	maxAgeSeconds    int64
}

// CORS creates middleware from a validated, restrictive cross-origin policy.
func CORS(config CORSConfig) (vial.Middleware, error) {
	policy, err := newCORSPolicy(config)
	if err != nil {
		return nil, err
	}

	return func(next vial.Handler) vial.Handler {
		return func(context *vial.Context) error {
			request := context.Request()
			origin := request.Header.Get("Origin")
			if origin == "" {
				return next(context)
			}

			responseHeaders := context.Response().Header()
			addVary(responseHeaders, "Origin")
			requestedMethod := strings.TrimSpace(request.Header.Get("Access-Control-Request-Method"))
			preflight := request.Method == http.MethodOptions && requestedMethod != ""
			if preflight {
				addVary(responseHeaders, "Access-Control-Request-Method")
				addVary(responseHeaders, "Access-Control-Request-Headers")
			}

			if !policy.allowsOrigin(origin) {
				if preflight {
					return vial.Forbidden("cors_forbidden", "CORS request is not allowed")
				}
				return next(context)
			}

			method := request.Method
			if preflight {
				method = strings.ToUpper(requestedMethod)
			}
			if !policy.allowsMethod(method) || (preflight && !policy.allowsHeaders(request.Header.Get("Access-Control-Request-Headers"))) {
				if preflight {
					return vial.Forbidden("cors_forbidden", "CORS request is not allowed")
				}
				return next(context)
			}

			policy.setOriginHeaders(responseHeaders, origin)
			if !preflight {
				if len(policy.exposedValues) > 0 {
					responseHeaders.Set("Access-Control-Expose-Headers", strings.Join(policy.exposedValues, ", "))
				}
				return next(context)
			}

			responseHeaders.Set("Access-Control-Allow-Methods", strings.Join(policy.methodValues, ", "))
			if len(policy.headerValues) > 0 {
				responseHeaders.Set("Access-Control-Allow-Headers", strings.Join(policy.headerValues, ", "))
			}
			if policy.maxAgeSeconds > 0 {
				responseHeaders.Set("Access-Control-Max-Age", strconv.FormatInt(policy.maxAgeSeconds, 10))
			}
			return context.NoContent(http.StatusNoContent)
		}
	}, nil
}

func newCORSPolicy(config CORSConfig) (corsPolicy, error) {
	if config.MaxAge < 0 {
		return corsPolicy{}, fmt.Errorf("cors max age cannot be negative")
	}

	origins, originValues, err := normalizeCORSValues("origin", config.AllowedOrigins, false)
	if err != nil {
		return corsPolicy{}, err
	}
	wildcardOrigin := false
	if _, ok := origins["*"]; ok {
		wildcardOrigin = true
		delete(origins, "*")
		if len(originValues) != 1 {
			return corsPolicy{}, fmt.Errorf("cors wildcard origin cannot be combined with exact origins")
		}
	}
	if wildcardOrigin && config.AllowCredentials {
		return corsPolicy{}, fmt.Errorf("cors wildcard origin cannot be combined with credentials")
	}

	methods := config.AllowedMethods
	if len(methods) == 0 {
		methods = []string{http.MethodGet, http.MethodHead, http.MethodPost}
	}
	methodSet, methodValues, err := normalizeCORSValues("method", methods, true)
	if err != nil {
		return corsPolicy{}, err
	}
	headerSet, headerValues, err := normalizeCORSValues("header", config.AllowedHeaders, false)
	if err != nil {
		return corsPolicy{}, err
	}
	_, exposedValues, err := normalizeCORSValues("exposed header", config.ExposedHeaders, false)
	if err != nil {
		return corsPolicy{}, err
	}

	return corsPolicy{
		origins:          origins,
		methods:          methodSet,
		headers:          headerSet,
		methodValues:     methodValues,
		headerValues:     headerValues,
		exposedValues:    exposedValues,
		wildcardOrigin:   wildcardOrigin,
		allowCredentials: config.AllowCredentials,
		maxAgeSeconds:    int64(config.MaxAge / time.Second),
	}, nil
}

func normalizeCORSValues(kind string, values []string, uppercase bool) (map[string]struct{}, []string, error) {
	set := make(map[string]struct{}, len(values))
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if uppercase {
			value = strings.ToUpper(value)
		}
		if value == "" || strings.ContainsAny(value, "\r\n") {
			return nil, nil, fmt.Errorf("cors %s cannot be empty or contain a newline", kind)
		}
		key := strings.ToLower(value)
		if _, exists := set[key]; exists {
			continue
		}
		set[key] = struct{}{}
		normalized = append(normalized, value)
	}
	return set, normalized, nil
}

func (policy corsPolicy) allowsOrigin(origin string) bool {
	if policy.wildcardOrigin {
		return true
	}
	_, ok := policy.origins[strings.ToLower(origin)]
	return ok
}

func (policy corsPolicy) allowsMethod(method string) bool {
	_, ok := policy.methods[strings.ToLower(method)]
	return ok
}

func (policy corsPolicy) allowsHeaders(header string) bool {
	for _, value := range strings.Split(header, ",") {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := policy.headers[strings.ToLower(value)]; !ok {
			return false
		}
	}
	return true
}

func (policy corsPolicy) setOriginHeaders(header http.Header, origin string) {
	if policy.wildcardOrigin {
		header.Set("Access-Control-Allow-Origin", "*")
	} else {
		header.Set("Access-Control-Allow-Origin", origin)
	}
	if policy.allowCredentials {
		header.Set("Access-Control-Allow-Credentials", "true")
	}
}

func addVary(header http.Header, value string) {
	for _, existing := range header.Values("Vary") {
		for _, item := range strings.Split(existing, ",") {
			if strings.EqualFold(strings.TrimSpace(item), value) {
				return
			}
		}
	}
	header.Add("Vary", value)
}
