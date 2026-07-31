package dev

import (
	"path/filepath"
	"testing"
)

func TestIgnoreMatcher(t *testing.T) {
	root := filepath.Join(string(filepath.Separator), "project")
	matcher := newIgnoreMatcher(root, []string{"generated", filepath.Join("internal", "fixtures")})

	tests := []struct {
		path string
		want bool
	}{
		{path: filepath.Join(root, "main.go"), want: false},
		{path: filepath.Join(root, ".git", "config"), want: true},
		{path: filepath.Join(root, ".vial", "bin", "app"), want: true},
		{path: filepath.Join(root, "pkg", "generated", "file.go"), want: true},
		{path: filepath.Join(root, "internal", "fixtures", "file.go"), want: true},
		{path: filepath.Join(root, "internal", "real", "file.go"), want: false},
	}

	for _, test := range tests {
		if got := matcher.Match(test.path); got != test.want {
			t.Errorf("Match(%q) = %v, want %v", test.path, got, test.want)
		}
	}
}

func TestRelevantSource(t *testing.T) {
	for _, path := range []string{"main.go", "go.mod", "go.sum", "go.work", "GOFILE.GO"} {
		if !isRelevantSource(path) {
			t.Errorf("expected %q to be relevant", path)
		}
	}
	for _, path := range []string{"README.md", "app.exe", "template.html"} {
		if isRelevantSource(path) {
			t.Errorf("expected %q to be ignored", path)
		}
	}
}
