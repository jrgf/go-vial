package vial_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/jrgf/go-vial"
)

type httpMiddlewareContextKey struct{}

func TestHTTPMiddlewareWrapsCompleteApplication(t *testing.T) {
	var order []string
	outer := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			order = append(order, "outer:before")
			writer.Header().Set("X-HTTP-Middleware", "wrapped")
			request = request.WithContext(context.WithValue(request.Context(), httpMiddlewareContextKey{}, "value"))
			next.ServeHTTP(writer, request)
			order = append(order, "outer:after")
		})
	}
	inner := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			order = append(order, "inner:before")
			next.ServeHTTP(writer, request)
			order = append(order, "inner:after")
		})
	}

	app := vial.New()
	app.UseHTTP(outer, inner)
	app.Get("/", func(contextValue *vial.Context) error {
		order = append(order, "handler")
		if got := contextValue.Request().Context().Value(httpMiddlewareContextKey{}); got != "value" {
			t.Fatalf("request context value = %v", got)
		}
		return vial.BadRequest("bad", "bad request")
	})

	response := httptest.NewRecorder()
	app.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
	if response.Code != http.StatusBadRequest || response.Header().Get("X-HTTP-Middleware") != "wrapped" {
		t.Fatalf("response: status=%d headers=%v", response.Code, response.Header())
	}
	want := []string{"outer:before", "inner:before", "handler", "inner:after", "outer:after"}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
}

func TestHTTPMiddlewareMayShortCircuit(t *testing.T) {
	app := vial.New()
	app.UseHTTP(func(http.Handler) http.Handler {
		return http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			writer.WriteHeader(http.StatusTeapot)
		})
	})
	app.Get("/", func(*vial.Context) error {
		t.Fatal("handler was called")
		return nil
	})

	response := httptest.NewRecorder()
	app.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
	if response.Code != http.StatusTeapot {
		t.Fatalf("status = %d", response.Code)
	}
}

func TestHTTPMiddlewareRejectsNilWrappedHandler(t *testing.T) {
	app := vial.New()
	app.UseHTTP(func(http.Handler) http.Handler { return nil })
	if err := app.Build(); err == nil {
		t.Fatal("expected build error")
	}
}
