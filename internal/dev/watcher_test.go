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
	t.Cleanup(func() {
		if err := watcher.Close(); err != nil {
			t.Errorf("close watcher: %v", err)
		}
	})

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

func TestWatcherIncludesAssetsAndHonorsExclusions(t *testing.T) {
	root := t.TempDir()
	files := []string{"main.go", "nested/view.html", "static/app.css", "static/private/secret.css", ".vial/out.css", "README.md"}
	for _, name := range files {
		filename := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(filename), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filename, []byte("before"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	watcher, err := NewWatcher(root, []string{"static/private"}, "*.html", "static/*.css", "*.sql")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = watcher.Close() })
	snapshot, err := watcher.scan()
	if err != nil || len(snapshot) != 3 {
		t.Fatalf("snapshot=%v error=%v", snapshot, err)
	}
	for _, name := range files[:3] {
		if _, ok := snapshot[filepath.Join(root, filepath.FromSlash(name))]; !ok {
			t.Errorf("missing watched file %q", name)
		}
	}
	asset := filepath.Join(root, "migration.sql")
	for _, content := range []string{"create table notes", "create table notes (id int)", ""} {
		if content == "" {
			err = os.Remove(asset)
		} else {
			err = os.WriteFile(asset, []byte(content), 0o644)
		}
		if err != nil {
			t.Fatal(err)
		}
		select {
		case change := <-watcher.Changes():
			if change.Path != asset {
				t.Fatalf("unexpected asset change %q", change.Path)
			}
		case err := <-watcher.Errors():
			t.Fatal(err)
		case <-time.After(3 * time.Second):
			t.Fatal("asset creation, modification, or deletion did not trigger a change")
		}
	}
}

func TestWatcherRejectsInvalidPatterns(t *testing.T) {
	for _, pattern := range []string{"", "[", "../*.html", "/templates/*.html"} {
		watcher, err := NewWatcher(t.TempDir(), nil, pattern)
		if err == nil {
			_ = watcher.Close()
			t.Errorf("accepted watch pattern %q", pattern)
		}
	}
}
