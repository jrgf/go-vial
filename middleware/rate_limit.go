package middleware

import (
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/jrgf/go-vial"
)

const (
	defaultRateLimitMaxKeys = 10_000
	maxRateLimitKeyBytes    = 512
)

// RateLimitConfig configures one in-process token bucket per key.
type RateLimitConfig struct {
	Requests int
	Window   time.Duration
	Burst    int
	MaxKeys  int
	Key      func(*vial.Context) (string, error)
}

type rateLimitBucket struct {
	tokens  float64
	updated time.Time
	seen    time.Time
}

type rateLimiter struct {
	mu          sync.Mutex
	requests    int
	window      time.Duration
	burst       int
	maxKeys     int
	buckets     map[string]rateLimitBucket
	nextCleanup time.Time
}

// RateLimit creates bounded, process-local rate-limiting middleware. The
// default key is Context.ClientIP, including its trusted-proxy policy.
func RateLimit(config RateLimitConfig) (vial.Middleware, error) {
	limiter, key, err := newRateLimiter(config)
	if err != nil {
		return nil, err
	}

	return func(next vial.Handler) vial.Handler {
		return func(context *vial.Context) error {
			value, err := key(context)
			if err != nil {
				return err
			}
			if value == "" || len(value) > maxRateLimitKeyBytes {
				return vial.InternalServerError(errors.New("rate limit key must contain 1 to 512 bytes"))
			}

			allowed, retryAfter := limiter.allow(value, time.Now())
			if allowed {
				return next(context)
			}

			responseErr := vial.NewHTTPError(http.StatusTooManyRequests, "rate_limited", "Too many requests")
			responseErr.Headers = make(http.Header)
			responseErr.Headers.Set("Retry-After", strconv.FormatInt(retryAfterSeconds(retryAfter), 10))
			return responseErr
		}
	}, nil
}

func newRateLimiter(config RateLimitConfig) (*rateLimiter, func(*vial.Context) (string, error), error) {
	if config.Requests <= 0 {
		return nil, nil, fmt.Errorf("rate limit requests must be positive")
	}
	if config.Window <= 0 {
		return nil, nil, fmt.Errorf("rate limit window must be positive")
	}
	if config.Burst < 0 {
		return nil, nil, fmt.Errorf("rate limit burst cannot be negative")
	}
	if config.Burst == 0 {
		config.Burst = config.Requests
	}
	if config.Burst > config.Requests {
		return nil, nil, fmt.Errorf("rate limit burst cannot exceed requests")
	}
	if config.MaxKeys < 0 {
		return nil, nil, fmt.Errorf("rate limit max keys cannot be negative")
	}
	if config.MaxKeys == 0 {
		config.MaxKeys = defaultRateLimitMaxKeys
	}
	key := config.Key
	if key == nil {
		key = func(context *vial.Context) (string, error) {
			address, err := context.ClientIP()
			if err != nil {
				return "", vial.BadRequest("invalid_client_address", "The client address is invalid")
			}
			return address.String(), nil
		}
	}

	return &rateLimiter{
		requests: config.Requests,
		window:   config.Window,
		burst:    config.Burst,
		maxKeys:  config.MaxKeys,
		buckets:  make(map[string]rateLimitBucket),
	}, key, nil
}

func (limiter *rateLimiter) allow(key string, now time.Time) (bool, time.Duration) {
	limiter.mu.Lock()
	defer limiter.mu.Unlock()

	limiter.cleanup(now)
	bucket, exists := limiter.buckets[key]
	if !exists {
		if len(limiter.buckets) >= limiter.maxKeys {
			return false, max(limiter.nextCleanup.Sub(now), time.Second)
		}
		bucket = rateLimitBucket{tokens: float64(limiter.burst), updated: now}
	}

	if elapsed := now.Sub(bucket.updated); elapsed > 0 {
		refill := float64(elapsed) * float64(limiter.requests) / float64(limiter.window)
		bucket.tokens = min(float64(limiter.burst), bucket.tokens+refill)
	}
	bucket.updated = now
	bucket.seen = now
	if bucket.tokens >= 1 {
		bucket.tokens--
		limiter.buckets[key] = bucket
		return true, 0
	}

	limiter.buckets[key] = bucket
	retryAfter := time.Duration(math.Ceil((1 - bucket.tokens) * float64(limiter.window) / float64(limiter.requests)))
	return false, max(retryAfter, time.Nanosecond)
}

func (limiter *rateLimiter) cleanup(now time.Time) {
	if limiter.nextCleanup.IsZero() {
		limiter.nextCleanup = now.Add(limiter.window)
		return
	}
	if now.Before(limiter.nextCleanup) {
		return
	}
	for key, bucket := range limiter.buckets {
		if now.Sub(bucket.seen) >= limiter.window {
			delete(limiter.buckets, key)
		}
	}
	limiter.nextCleanup = now.Add(limiter.window)
}

func retryAfterSeconds(duration time.Duration) int64 {
	return max(int64(math.Ceil(duration.Seconds())), 1)
}
