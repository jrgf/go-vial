package dev

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWatcherReportsGoSourceChange(t *testing.T) {
	root := t.TempDir()
	watcher, err := NewWatcher(root, nil)
	if err != nil {
		t.Fatalf("new watcher: %v", err)
	}
	defer watcher.Close()

	path := filepath.Join(root, "main.go")
	if err := os.WriteFile(path, []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	select {
	case change := <-watcher.Changes():
		if filepath.Clean(change.Path) != filepath.Clean(path) {
			t.Fatalf("unexpected changed path %q", change.Path)
		}
	case err := <-watcher.Errors():
		t.Fatalf("watcher error: %v", err)
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for source change")
	}
}
