package dev

import (
	"path/filepath"
	"testing"
)

func TestWatcherNormalizesNativeSeparators(t *testing.T) {
	root := t.TempDir()
	w, err := NewWatcher(root, nil, `templates\*.html`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = w.Close() }()
	if !w.relevant(filepath.Join(root, "templates", "index.html")) {
		t.Fatal("native pattern did not match")
	}
}
