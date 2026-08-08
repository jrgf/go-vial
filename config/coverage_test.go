package config

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

type coverageText string

func (value *coverageText) UnmarshalText(text []byte) error {
	if string(text) == "bad" {
		return errors.New("bad text")
	}
	*value = coverageText(text)
	return nil
}

func TestCoverageLoadErrors(t *testing.T) {
	var destination struct{}
	if err := Load(&destination, nil, File(""), Environ(nil)); err == nil {
		t.Fatal("expected empty path error")
	}

	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte("{} invalid"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Load(&destination, File(path), Environ(nil)); err == nil {
		t.Fatal("expected trailing JSON error")
	}
}

func TestCoverageEnvironmentTraversal(t *testing.T) {
	type nested struct {
		Value string `env:"VALUE"`
	}
	tests := []struct {
		name        string
		destination any
		environ     []string
		wantErr     bool
	}{
		{"empty tag", &struct {
			Value string `env:""`
		}{}, nil, true},
		{"ignored and missing", &struct {
			Ignored string `env:"-"`
			Missing string `env:"MISSING"`
			private string
		}{}, []string{"malformed"}, false},
		{"nested pointer", &struct{ Nested *nested }{Nested: &nested{}}, []string{"VALUE=set"}, false},
		{"nil nested pointer", &struct{ Nested *nested }{}, []string{"VALUE=set"}, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := Load(test.destination, Environ(test.environ))
			if (err != nil) != test.wantErr {
				t.Fatalf("Load() error = %v", err)
			}
		})
	}
}

func TestCoverageSetText(t *testing.T) {
	text := coverageText("")
	if err := setText(reflect.ValueOf(&text).Elem(), "ok"); err != nil || text != "ok" {
		t.Fatalf("text = %q, %v", text, err)
	}
	var pointer *coverageText
	if err := setText(reflect.ValueOf(&pointer).Elem(), "pointer"); err != nil || pointer == nil || *pointer != "pointer" {
		t.Fatalf("pointer = %#v, %v", pointer, err)
	}
	var number *int
	if err := setText(reflect.ValueOf(&number).Elem(), "42"); err != nil || number == nil || *number != 42 {
		t.Fatalf("number pointer = %#v, %v", number, err)
	}

	values := []any{new(bool), new(int8), new(uint8), new(float32)}
	valid := []string{"true", "12", "12", "1.5"}
	invalid := []string{"not-bool", "999", "999", "not-float"}
	for index, destination := range values {
		field := reflect.ValueOf(destination).Elem()
		if err := setText(field, valid[index]); err != nil {
			t.Fatalf("valid %s: %v", field.Kind(), err)
		}
		if err := setText(field, invalid[index]); err == nil {
			t.Fatalf("invalid %s accepted", field.Kind())
		}
	}

	unsupported := struct{}{}
	if err := setText(reflect.ValueOf(&unsupported).Elem(), "value"); err == nil {
		t.Fatal("unsupported struct accepted")
	}
}

func TestCoverageNestedPointerError(t *testing.T) {
	type invalid struct {
		Value string `env:""`
	}
	destination := struct{ Nested *invalid }{Nested: &invalid{}}
	if err := Load(&destination, Environ(nil)); err == nil {
		t.Fatal("expected nested pointer error")
	}
}
