package dev

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestBuilderCreatesExecutable(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/testapp\n\ngo 1.23.0\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatalf("write main.go: %v", err)
	}

	var output bytes.Buffer
	config, err := (Config{Root: root, Target: ".", Stdout: &output, Stderr: &output}).withDefaults()
	if err != nil {
		t.Fatalf("config: %v", err)
	}

	executable, err := NewBuilder(config).Build(context.Background())
	if err != nil {
		t.Fatalf("build: %v\n%s", err, output.String())
	}
	if runtime.GOOS == "windows" && filepath.Ext(executable) != ".exe" {
		t.Fatalf("expected Windows executable suffix, got %q", executable)
	}
	if _, err := os.Stat(executable); err != nil {
		t.Fatalf("stat executable: %v", err)
	}
}

func TestBuilderCreatesExecutableFromNestedModule(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/root\n\ngo 1.23.0\n"), 0o644); err != nil {
		t.Fatalf("write root go.mod: %v", err)
	}
	nested := filepath.Join(root, "nested")
	if err := os.Mkdir(nested, 0o755); err != nil {
		t.Fatalf("create nested module: %v", err)
	}
	if err := os.WriteFile(filepath.Join(nested, "go.mod"), []byte("module example.com/nested\n\ngo 1.23.0\n"), 0o644); err != nil {
		t.Fatalf("write nested go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(nested, "main.go"), []byte("package main\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatalf("write nested main.go: %v", err)
	}

	var output bytes.Buffer
	config, err := (Config{Root: root, Target: "./nested", Stdout: &output, Stderr: &output}).withDefaults()
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	if _, err := NewBuilder(config).Build(context.Background()); err != nil {
		t.Fatalf("build nested module: %v\n%s", err, output.String())
	}
}
