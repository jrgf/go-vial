package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"net/http"
	"net/http/httptest"
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
		{Method: "GET", Path: "/", Pattern: "GET /{$}", Name: "home"},
		{Method: "GET", Path: "/users/{id}", Pattern: "GET /users/{id}", Name: "users.show"},
		{Method: "GET", Path: "/search", Pattern: "GET /search", Name: "search"},
		{Method: "POST", Path: "/submit", Pattern: "POST /submit", Name: "submit"},
	}
	if !reflect.DeepEqual(routes, want) {
		t.Fatalf("routes = %#v, want %#v", routes, want)
	}
}

func TestRunDoctor(t *testing.T) {
	var output bytes.Buffer
	if err := runDoctor([]string{"../../examples/config"}, &output); err != nil {
		t.Fatalf("run doctor: %v", err)
	}
	if got := output.String(); got != "vial doctor: ok (routes: 1)\n" {
		t.Fatalf("doctor output = %q", got)
	}
}

func TestRunLoad(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)

	var output bytes.Buffer
	var progress bytes.Buffer
	if err := runLoad([]string{
		"--workers", "2",
		"--duration", "20ms",
		"--timeout", "100ms",
		server.URL,
	}, &output, &progress); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"Requests:", "Throughput:", "Latency:", "Status: 204="} {
		if !strings.Contains(output.String(), value) {
			t.Errorf("load output does not contain %q: %s", value, output.String())
		}
	}
	if !strings.Contains(progress.String(), "[vial] load progress: 100%") {
		t.Fatalf("load progress missing completion: %s", progress.String())
	}
}

func TestRunLoadThresholdFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)

	var output bytes.Buffer
	err := runLoad([]string{
		"--workers", "1",
		"--duration", "10ms",
		"--timeout", "100ms",
		"--max-error-rate", "0",
		server.URL,
	}, &output, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "thresholds failed") {
		t.Fatalf("threshold error=%v", err)
	}
	if !strings.Contains(output.String(), "500=") {
		t.Fatalf("summary missing status: %s", output.String())
	}
}

func TestWriteRoutesTable(t *testing.T) {
	var output bytes.Buffer
	err := writeRoutes(&output, []vial.Route{
		{Method: "GET", Path: "/users", Name: "users.index", Module: "users"},
		{Path: "GET /health"},
	}, false)
	if err != nil {
		t.Fatalf("write routes: %v", err)
	}
	for _, value := range []string{"METHOD", "PATH", "NAME", "MODULE", "GET", "/users", "users.index", "users", "*", "GET /health", "-"} {
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

func TestRunDoctorValidatesArguments(t *testing.T) {
	if err := runDoctor([]string{"--unknown"}, &bytes.Buffer{}); err == nil {
		t.Fatal("expected unknown flag error")
	}
	if err := runDoctor([]string{"one", "two"}, &bytes.Buffer{}); err == nil {
		t.Fatal("expected multiple package error")
	}
}

func TestRunLoadValidatesArguments(t *testing.T) {
	for _, arguments := range [][]string{{"--unknown"}, nil, {"one", "two"}, {"--workers", "0", "http://example.com"}} {
		if err := runLoad(arguments, &bytes.Buffer{}, &bytes.Buffer{}); err == nil {
			t.Fatalf("expected load error for %q", arguments)
		}
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
