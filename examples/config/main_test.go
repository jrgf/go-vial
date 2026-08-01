package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

func TestConfigValidation(t *testing.T) {
	if err := (&applicationConfig{}).Validate(); err == nil {
		t.Fatal("expected validation error")
	}
}
