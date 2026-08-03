package vial_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jrgf/go-vial"
)

func FuzzClientIPForwarding(fuzz *testing.F) {
	fuzz.Add("for=192.0.2.1", "192.0.2.1")
	fuzz.Add(`for="[2001:db8::1]:443"`, "2001:db8::1")
	fuzz.Add("garbage", "not-an-ip")
	fuzz.Fuzz(func(t *testing.T, forwarded, xForwardedFor string) {
		if len(forwarded)+len(xForwardedFor) > 8<<10 {
			t.Skip()
		}
		app := vial.New(vial.WithTrustedProxies("10.0.0.0/8"))
		app.Get("/", func(context *vial.Context) error {
			_, _ = context.ClientIP()
			return context.NoContent(http.StatusNoContent)
		})
		request := httptest.NewRequest(http.MethodGet, "/", nil)
		request.RemoteAddr = "10.0.0.1:443"
		request.Header.Set("Forwarded", forwarded)
		request.Header.Set("X-Forwarded-For", xForwardedFor)
		app.ServeHTTP(httptest.NewRecorder(), request)
	})
}
