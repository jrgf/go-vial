package vial_test

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	"github.com/jrgf/go-vial"
)

func TestClientIPTrustBoundaryAndForwardingChains(t *testing.T) {
	tests := []struct {
		name    string
		trusted []string
		remote  string
		headers map[string]string
		want    string
		wantErr bool
	}{
		{
			name: "forwarding ignored by default", remote: "192.0.2.10:1234",
			headers: map[string]string{"X-Forwarded-For": "203.0.113.9"}, want: "192.0.2.10",
		},
		{
			name: "untrusted peer malformed forwarding ignored", remote: "192.0.2.10:1234",
			headers: map[string]string{"Forwarded": "for=garbage"}, want: "192.0.2.10",
		},
		{
			name: "trusted CIDR walks right to left", trusted: []string{"10.0.0.0/8"}, remote: "10.0.0.3:443",
			headers: map[string]string{"X-Forwarded-For": "203.0.113.9, 198.51.100.7, 10.0.0.2"}, want: "198.51.100.7",
		},
		{
			name: "Forwarded chain", trusted: []string{"10.0.0.0/8"}, remote: "10.0.0.3:443",
			headers: map[string]string{"Forwarded": "for=198.51.100.8;proto=https, for=10.0.0.2"}, want: "198.51.100.8",
		},
		{
			name: "quoted IPv6 Forwarded", trusted: []string{"2001:db8::2"}, remote: "[2001:db8::2]:443",
			headers: map[string]string{"Forwarded": `for="[2001:db8:1::7]:4711"`}, want: "2001:db8:1::7",
		},
		{
			name: "X-Real-IP fallback", trusted: []string{"10.0.0.1"}, remote: "10.0.0.1:443",
			headers: map[string]string{"X-Real-IP": "198.51.100.9"}, want: "198.51.100.9",
		},
		{
			name: "malformed trusted forwarding rejected", trusted: []string{"10.0.0.1"}, remote: "10.0.0.1:443",
			headers: map[string]string{"X-Forwarded-For": "not-an-ip"}, wantErr: true,
		},
		{name: "malformed remote rejected", remote: "not-an-address", wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			app := vial.New(vial.WithTrustedProxies(test.trusted...))
			var got netip.Addr
			var gotErr error
			app.Get("/", func(context *vial.Context) error {
				got, gotErr = context.ClientIP()
				return context.NoContent(http.StatusNoContent)
			})
			request := httptest.NewRequest(http.MethodGet, "/", nil)
			request.RemoteAddr = test.remote
			for name, value := range test.headers {
				request.Header.Set(name, value)
			}
			response := httptest.NewRecorder()
			app.ServeHTTP(response, request)
			if test.wantErr {
				if gotErr == nil {
					t.Fatalf("expected client IP error, got %s", got)
				}
				return
			}
			if gotErr != nil || got.String() != test.want {
				t.Fatalf("client IP=%s error=%v, want %s", got, gotErr, test.want)
			}
		})
	}
}

func TestTrustedProxyConfigurationIsValidated(t *testing.T) {
	app := vial.New(vial.WithTrustedProxies("10.0.0.0/8", "not-a-network"))
	if err := app.Build(); err == nil || !strings.Contains(err.Error(), "WithTrustedProxies") {
		t.Fatalf("invalid trusted proxy returned %v", err)
	}
}
