package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/jrgf/go-vial/config"
)

func TestConfigApplication(t *testing.T) {
	configuration := applicationConfig{
		Environment: "test",
		HTTP:        config.HTTP{Port: 9090},
	}
	response := httptest.NewRecorder()
	newApp(configuration).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	var body map[string]string
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	want := map[string]string{
		"environment": "test",
		"address":     "127.0.0.1:9090",
	}
	if !reflect.DeepEqual(body, want) {
		t.Fatalf("body = %#v, want %#v", body, want)
	}
}

func TestConfigExampleMain(t *testing.T) {
	t.Setenv("VIAL_ENV", "test")
	t.Setenv("VIAL_HTTP_PORT", "0")
	t.Setenv("VIAL_ROUTES_OUTPUT", filepath.Join(t.TempDir(), "routes.json"))
	main()
}

func TestConfigValidation(t *testing.T) {
	if err := (&applicationConfig{}).Validate(); err == nil {
		t.Fatal("expected validation error")
	}
	if err := (&applicationConfig{Environment: "test"}).Validate(); err != nil {
		t.Fatalf("valid configuration: %v", err)
	}
}
