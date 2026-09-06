package middleware

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jrgf/go-vial"
)

func TestRateLimitRejectsExcessRequests(t *testing.T) {
	limit, err := RateLimit(RateLimitConfig{Requests: 2, Window: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	app := vial.New()
	app.Use(limit)
	app.Get("/", func(context *vial.Context) error {
		return context.NoContent(http.StatusNoContent)
	})

	for requestNumber := 1; requestNumber <= 3; requestNumber++ {
		response := httptest.NewRecorder()
		app.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
		if requestNumber <= 2 && response.Code != http.StatusNoContent {
			t.Fatalf("request %d status = %d", requestNumber, response.Code)
		}
		if requestNumber == 3 {
			if response.Code != http.StatusTooManyRequests || !strings.Contains(response.Body.String(), `"code":"rate_limited"`) {
				t.Fatalf("limited response: status=%d body=%s", response.Code, response.Body.String())
			}
			seconds, parseErr := strconv.Atoi(response.Header().Get("Retry-After"))
			if parseErr != nil || seconds < 1 {
				t.Fatalf("Retry-After = %q", response.Header().Get("Retry-After"))
			}
		}
	}
}

func TestRateLimitUsesTrustedClientIP(t *testing.T) {
	limit, err := RateLimit(RateLimitConfig{Requests: 1, Window: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	app := vial.New(vial.WithTrustedProxies("10.0.0.0/8"))
	app.Use(limit)
	app.Get("/", func(context *vial.Context) error {
		return context.NoContent(http.StatusNoContent)
	})

	for _, client := range []string{"192.0.2.1", "198.51.100.1"} {
		request := httptest.NewRequest(http.MethodGet, "/", nil)
		request.RemoteAddr = "10.0.0.1:443"
		request.Header.Set("X-Forwarded-For", client)
		response := httptest.NewRecorder()
		app.ServeHTTP(response, request)
		if response.Code != http.StatusNoContent {
			t.Fatalf("client %s status = %d", client, response.Code)
		}
	}

	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.RemoteAddr = "10.0.0.1:443"
	request.Header.Set("X-Forwarded-For", "invalid")
	response := httptest.NewRecorder()
	app.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), `"code":"invalid_client_address"`) {
		t.Fatalf("invalid address response: status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestRateLimiterRefillCleanupAndCapacity(t *testing.T) {
	limiter, _, err := newRateLimiter(RateLimitConfig{Requests: 2, Window: time.Minute, Burst: 1, MaxKeys: 1})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(100, 0)
	if allowed, _ := limiter.allow("first", now); !allowed {
		t.Fatal("first request was denied")
	}
	if allowed, retry := limiter.allow("first", now); allowed || retry != 30*time.Second {
		t.Fatalf("second request: allowed=%v retry=%v", allowed, retry)
	}
	if allowed, _ := limiter.allow("second", now); allowed {
		t.Fatal("new key exceeded capacity")
	}
	if allowed, _ := limiter.allow("first", now.Add(30*time.Second)); !allowed {
		t.Fatal("refilled request was denied")
	}
	if allowed, _ := limiter.allow("second", now.Add(91*time.Second)); !allowed {
		t.Fatal("inactive key was not cleaned")
	}
}

func TestRateLimitConcurrentRequests(t *testing.T) {
	limit, err := RateLimit(RateLimitConfig{
		Requests: 10,
		Window:   time.Hour,
		Key: func(*vial.Context) (string, error) {
			return "shared", nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	app := vial.New()
	app.Use(limit)
	var accepted atomic.Int64
	app.Get("/", func(context *vial.Context) error {
		accepted.Add(1)
		return context.NoContent(http.StatusNoContent)
	})

	var wait sync.WaitGroup
	for range 100 {
		wait.Go(func() {
			app.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
		})
	}
	wait.Wait()
	if got := accepted.Load(); got != 10 {
		t.Fatalf("accepted = %d, want 10", got)
	}
}

func TestRateLimitValidationAndCustomKeyErrors(t *testing.T) {
	configs := []RateLimitConfig{
		{},
		{Requests: 1},
		{Requests: 1, Window: time.Second, Burst: -1},
		{Requests: 1, Window: time.Second, Burst: 2},
		{Requests: 1, Window: time.Second, MaxKeys: -1},
	}
	for _, config := range configs {
		if _, err := RateLimit(config); err == nil {
			t.Fatalf("config %#v succeeded", config)
		}
	}

	wantErr := errors.New("key failed")
	limit, err := RateLimit(RateLimitConfig{
		Requests: 1,
		Window:   time.Second,
		Key: func(*vial.Context) (string, error) {
			return "", wantErr
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	app := vial.New()
	app.Use(limit)
	app.Get("/", func(context *vial.Context) error { return nil })
	response := httptest.NewRecorder()
	app.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("custom key error status = %d", response.Code)
	}
}
