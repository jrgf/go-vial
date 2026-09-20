package middleware

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jrgf/go-vial"
)

func TestRateLimiterCapacityDoesNotRejectEveryNewKey(t *testing.T) {
	l, _, err := newRateLimiter(RateLimitConfig{Requests: 1, Window: time.Minute, MaxKeys: 1})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(100, 0)
	l.allow("attacker", now)
	l.allow("attacker", now.Add(time.Second))
	if !l.buckets["attacker"].seen.Equal(now) {
		t.Error("denied request refreshed retention")
	}
	if ok, _ := l.allow("new-client", now.Add(time.Second)); !ok {
		t.Error("capacity alone rejected new client")
	}
	if ok, _ := l.allow("another-client", now.Add(time.Second)); ok {
		t.Error("overflow keys bypassed rate limit")
	}
	if len(l.buckets) != 1 {
		t.Fatal("unbounded key table")
	}
}

func TestRateLimitGroupsIPv6Subnet(t *testing.T) {
	limit, err := RateLimit(RateLimitConfig{Requests: 1, Window: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	app := vial.New()
	app.Use(limit)
	app.Get("/", func(c *vial.Context) error { return c.NoContent(204) })
	for i, address := range []string{"[2001:db8::1]:1234", "[2001:db8::2]:1234", "[2001:db8:0:1::1]:1234"} {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = address
		w := httptest.NewRecorder()
		app.ServeHTTP(w, r)
		want := 204
		if i == 1 {
			want = 429
		}
		if w.Code != want {
			t.Errorf("address=%s status=%d want=%d", address, w.Code, want)
		}
	}
}
