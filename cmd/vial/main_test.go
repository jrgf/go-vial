package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/jrgf/go-vial"
)

func TestSplitApplicationArguments(t *testing.T) {
	framework, application := splitApplicationArguments([]string{
		"--verbose",
		"./cmd/server",
		"--",
		"--config",
		"dev.json",
	})

	if !reflect.DeepEqual(framework, []string{"--verbose", "./cmd/server"}) {
		t.Fatalf("unexpected framework arguments %#v", framework)
	}
	if !reflect.DeepEqual(application, []string{"--config", "dev.json"}) {
		t.Fatalf("unexpected application arguments %#v", application)
	}
}

func TestStringList(t *testing.T) {
	var values stringList
	if err := values.Set("generated"); err != nil {
		t.Fatalf("set: %v", err)
	}
	if err := values.Set("fixtures"); err != nil {
		t.Fatalf("set: %v", err)
	}
	if got := values.String(); got != "generated,fixtures" {
		t.Fatalf("String() = %q", got)
	}
}

func TestRunCommands(t *testing.T) {
	for _, arguments := range [][]string{nil, {"help"}, {"version"}} {
		if err := run(arguments); err != nil {
			t.Errorf("run(%q): %v", arguments, err)
		}
	}
	if err := run([]string{"unknown"}); err == nil || !strings.Contains(err.Error(), "unknown command") {
		t.Fatalf("unexpected unknown command error %v", err)
	}
}

func TestRunRoutes(t *testing.T) {
	var output bytes.Buffer
	if err := runRoutes([]string{"--json", "../../examples/hello"}, &output); err != nil {
		t.Fatalf("run routes: %v", err)
	}

	var routes []vial.Route
	if err := json.Unmarshal(output.Bytes(), &routes); err != nil {
		t.Fatalf("decode routes: %v", err)
	}
	want := []vial.Route{
		{Method: "GET", Path: "/", Pattern: "GET /{$}"},
		{Method: "GET", Path: "/users/{id}", Pattern: "GET /users/{id}"},
		{Method: "GET", Path: "/search", Pattern: "GET /search"},
		{Method: "POST", Path: "/submit", Pattern: "POST /submit"},
	}
	if !reflect.DeepEqual(routes, want) {
		t.Fatalf("routes = %#v, want %#v", routes, want)
	}
}

func TestWriteRoutesTable(t *testing.T) {
	var output bytes.Buffer
	err := writeRoutes(&output, []vial.Route{
		{Method: "GET", Path: "/users"},
		{Path: "GET /health"},
	}, false)
	if err != nil {
		t.Fatalf("write routes: %v", err)
	}
	for _, value := range []string{"METHOD", "GET", "/users", "*", "GET /health"} {
		if !strings.Contains(output.String(), value) {
			t.Errorf("table does not contain %q: %q", value, output.String())
		}
	}
}

func TestRunRoutesValidatesArguments(t *testing.T) {
	if err := runRoutes([]string{"--unknown"}, &bytes.Buffer{}); err == nil {
		t.Fatal("expected unknown flag error")
	}
	if err := runRoutes([]string{"one", "two"}, &bytes.Buffer{}); err == nil {
		t.Fatal("expected multiple package error")
	}
}

func TestRunDevValidatesArguments(t *testing.T) {
	if err := runDev([]string{"--unknown"}); err == nil {
		t.Fatal("expected unknown flag error")
	}
	if err := runDev([]string{"one", "two"}); err == nil {
		t.Fatal("expected multiple package error")
	}
	if err := runDev([]string{"--help"}); !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("help error = %v", err)
	}

	missingRoot := filepath.Join(t.TempDir(), "missing")
	err := runDev([]string{
		"--root", missingRoot,
		"--exclude", "generated",
		"--verbose",
		"./cmd/server",
		"--",
		"--config", "dev.json",
	})
	if err == nil || !strings.Contains(err.Error(), "inspect project root") {
		t.Fatalf("unexpected root error %v", err)
	}
}
