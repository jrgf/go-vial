package main

import (
	"errors"
	"flag"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
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
