package config_test

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jrgf/go-vial/config"
)

type logLevel string

func (level *logLevel) UnmarshalText(value []byte) error {
	switch string(value) {
	case "debug", "info":
		*level = logLevel(value)
		return nil
	default:
		return errors.New("invalid log level")
	}
}

type applicationConfig struct {
	Environment string `json:"environment" env:"VIAL_ENV"`
	HTTP        struct {
		Address string        `json:"address" env:"VIAL_HTTP_ADDRESS"`
		Port    uint16        `json:"port" env:"VIAL_HTTP_PORT"`
		Timeout time.Duration `json:"timeout" env:"VIAL_HTTP_TIMEOUT"`
	} `json:"http"`
	Enabled bool      `json:"enabled" env:"VIAL_ENABLED"`
	Ratio   float64   `json:"ratio" env:"VIAL_RATIO"`
	Level   logLevel  `json:"level" env:"VIAL_LOG_LEVEL"`
	Started time.Time `json:"started" env:"VIAL_STARTED"`
}

func TestLoadPrecedenceAndTypes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	data := []byte(`{
  "environment": "file",
  "http": {"address": ":9000", "port": 9000},
  "enabled": false
}`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	configuration := applicationConfig{Environment: "development", Enabled: false, Ratio: 1.5}
	configuration.HTTP.Address = ":8080"
	configuration.HTTP.Port = 8080
	configuration.HTTP.Timeout = 5 * time.Second

	err := config.Load(
		&configuration,
		config.File(path),
		config.Environ([]string{
			"VIAL_ENV=production",
			"VIAL_HTTP_TIMEOUT=2s",
			"VIAL_ENABLED=true",
			"VIAL_RATIO=2.5",
			"VIAL_LOG_LEVEL=debug",
			"VIAL_STARTED=2026-08-01T12:00:00Z",
		}),
	)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	wantStarted := time.Date(2026, time.August, 1, 12, 0, 0, 0, time.UTC)
	if configuration.Environment != "production" ||
		configuration.HTTP.Address != ":9000" ||
		configuration.HTTP.Port != 9000 ||
		configuration.HTTP.Timeout != 2*time.Second ||
		!configuration.Enabled ||
		configuration.Ratio != 2.5 ||
		configuration.Level != "debug" ||
		!configuration.Started.Equal(wantStarted) {
		t.Fatalf("unexpected configuration: %#v", configuration)
	}
}

func TestLoadFiles(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing.json")
	var configuration applicationConfig
	if err := config.Load(&configuration, config.OptionalFile(missing), config.Environ(nil)); err != nil {
		t.Fatalf("load optional file: %v", err)
	}
	if err := config.Load(&configuration, config.File(missing), config.Environ(nil)); err == nil {
		t.Fatal("expected required file error")
	}
}

func TestLoadRejectsUnknownJSONAndMultipleValues(t *testing.T) {
	tests := []struct {
		name string
		data string
		want string
	}{
		{name: "unknown field", data: `{"unknown": true}`, want: "unknown field"},
		{name: "multiple values", data: `{} {}`, want: "multiple JSON values"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			if err := os.WriteFile(path, []byte(test.data), 0o600); err != nil {
				t.Fatalf("write config: %v", err)
			}
			var configuration applicationConfig
			err := config.Load(&configuration, config.File(path), config.Environ(nil))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("load error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestEnvironmentErrorsDoNotExposeValues(t *testing.T) {
	const secret = "do-not-print-this-value"
	var configuration applicationConfig
	err := config.Load(
		&configuration,
		config.Environ([]string{"VIAL_HTTP_TIMEOUT=" + secret}),
	)
	if err == nil {
		t.Fatal("expected environment parsing error")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("error exposes configuration value: %v", err)
	}
	for _, part := range []string{"VIAL_HTTP_TIMEOUT", "HTTP.Timeout", "time.Duration"} {
		if !strings.Contains(err.Error(), part) {
			t.Errorf("error %q does not identify %q", err, part)
		}
	}
}

var errInvalidPort = errors.New("port is required")

type validatedConfig struct {
	Port int `env:"VIAL_PORT"`
}

func (configuration *validatedConfig) Validate() error {
	if configuration.Port <= 0 {
		return errInvalidPort
	}
	return nil
}

func TestLoadValidatesAfterEnvironment(t *testing.T) {
	tests := []struct {
		name    string
		environ []string
		want    validatedConfig
		wantErr error
	}{
		{name: "valid", environ: []string{"VIAL_PORT=8080"}, want: validatedConfig{Port: 8080}},
		{name: "invalid", environ: nil, wantErr: errInvalidPort},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var configuration validatedConfig
			err := config.Load(&configuration, config.Environ(test.environ))
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("load error = %v, want %v", err, test.wantErr)
			}
			if !reflect.DeepEqual(configuration, test.want) {
				t.Fatalf("configuration = %#v, want %#v", configuration, test.want)
			}
		})
	}
}

func TestLoadValidatesDestination(t *testing.T) {
	tests := []struct {
		name        string
		destination any
	}{
		{name: "nil", destination: nil},
		{name: "value", destination: applicationConfig{}},
		{name: "nil pointer", destination: (*applicationConfig)(nil)},
		{name: "non struct", destination: new(string)},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := config.Load(test.destination, config.Environ(nil)); err == nil {
				t.Fatal("expected destination error")
			}
		})
	}
}
