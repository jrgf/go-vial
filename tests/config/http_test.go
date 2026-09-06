package config_test

import (
	"testing"

	"github.com/jrgf/go-vial/config"
)

func TestHTTPAddress(t *testing.T) {
	tests := []struct {
		name          string
		configuration config.HTTP
		want          string
	}{
		{name: "defaults", want: "127.0.0.1:8080"},
		{name: "port", configuration: config.HTTP{Port: 9000}, want: "127.0.0.1:9000"},
		{name: "host and port", configuration: config.HTTP{Host: "0.0.0.0", Port: 80}, want: "0.0.0.0:80"},
		{name: "IPv6", configuration: config.HTTP{Host: "::1", Port: 8081}, want: "[::1]:8081"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := test.configuration.Address(); got != test.want {
				t.Fatalf("Address() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestHTTPLoadsPortFromEnvironment(t *testing.T) {
	var configuration struct {
		HTTP config.HTTP `json:"http"`
	}
	if err := config.Load(
		&configuration,
		config.Environ([]string{"VIAL_HTTP_PORT=9090"}),
	); err != nil {
		t.Fatalf("load: %v", err)
	}
	if got := configuration.HTTP.Address(); got != "127.0.0.1:9090" {
		t.Fatalf("Address() = %q, want %q", got, "127.0.0.1:9090")
	}
}
