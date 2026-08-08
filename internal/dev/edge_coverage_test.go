package dev

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestCoverageIgnoreMatcher(t *testing.T) {
	root := t.TempDir()
	matcher := newIgnoreMatcher(root, []string{"", ".", "./cache"})
	if len(matcher.extras) != 1 || matcher.extras[0] != "cache" {
		t.Fatalf("extras = %#v", matcher.extras)
	}
	if !matcher.Match(filepath.Join(root, "..", "outside.go")) {
		t.Fatal("outside path was not ignored")
	}
}

func TestCoverageMissingWorkingDirectory(t *testing.T) {
	original, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(t.TempDir(), "removed")
	if err := os.Mkdir(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(directory); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(directory); err != nil {
		_ = os.Chdir(original)
		t.Fatal(err)
	}
	_, configErr := (Config{}).withDefaults()
	_, _, _ = ResolvePackage("", ".")
	if err := os.Chdir(original); err != nil {
		t.Fatal(err)
	}
	if configErr == nil {
		t.Fatal("deleted working directory was accepted")
	}
}

func TestCoverageResolverAndWatcherErrors(t *testing.T) {
	root := t.TempDir()
	if directory, target, err := ResolvePackage(root, "."); err != nil || directory != root || target != "." {
		t.Fatalf("module-free package = %q %q, %v", directory, target, err)
	}
	if _, _, err := ResolvePackage(root, "\x00"); err == nil {
		t.Fatal("expected invalid target error")
	}
	if watcher, err := NewWatcher("\x00", nil); err == nil {
		_ = watcher.Close()
		t.Fatal("expected invalid watcher root error")
	}
}

func TestCoverageInjectedSystemErrors(t *testing.T) {
	originalGetwd, originalAbsolute := getWorkingDirectory, makeAbsolute
	originalRelative, originalStat := makeRelative, fileStat
	originalWalk, originalKill, originalWatcher := walkDirectory, killProcess, newSourceWatcher
	t.Cleanup(func() {
		getWorkingDirectory, makeAbsolute = originalGetwd, originalAbsolute
		makeRelative, fileStat = originalRelative, originalStat
		walkDirectory, killProcess, newSourceWatcher = originalWalk, originalKill, originalWatcher
	})

	want := errors.New("system failed")
	getWorkingDirectory = func() (string, error) { return "", want }
	if _, _, err := ResolvePackage("", "."); !errors.Is(err, want) {
		t.Fatalf("ResolvePackage getwd error = %v", err)
	}
	if _, err := (Config{}).withDefaults(); !errors.Is(err, want) {
		t.Fatalf("config getwd error = %v", err)
	}
	getWorkingDirectory = originalGetwd

	makeAbsolute = func(string) (string, error) { return "", want }
	if _, err := NewBuilder(Config{}).Build(context.Background()); !errors.Is(err, want) {
		t.Fatalf("builder absolute error = %v", err)
	}
	if _, err := (Config{Root: t.TempDir()}).withDefaults(); !errors.Is(err, want) {
		t.Fatalf("config absolute error = %v", err)
	}
	if _, err := NewWatcher(t.TempDir(), nil); !errors.Is(err, want) {
		t.Fatalf("watcher absolute error = %v", err)
	}
	makeAbsolute = originalAbsolute

	module := t.TempDir()
	if err := os.WriteFile(filepath.Join(module, "go.mod"), []byte("module test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	makeRelative = func(string, string) (string, error) { return "", want }
	if _, _, err := ResolvePackage(module, "."); !errors.Is(err, want) {
		t.Fatalf("relative error = %v", err)
	}
	makeRelative = originalRelative

	fileStat = func(path string) (fs.FileInfo, error) {
		if filepath.Base(path) == "go.mod" {
			return nil, want
		}
		return originalStat(path)
	}
	if _, _, err := ResolvePackage(t.TempDir(), "."); !errors.Is(err, want) {
		t.Fatalf("module stat error = %v", err)
	}
	fileStat = originalStat

	killProcess = func(*exec.Cmd) error { return want }
	process := &Process{command: &exec.Cmd{}, done: make(chan struct{})}
	if err := process.Stop(time.Millisecond); !errors.Is(err, want) {
		t.Fatalf("kill error = %v", err)
	}
	killProcess = originalKill

	newSourceWatcher = func(string, []string) (*Watcher, error) { return nil, want }
	if _, err := NewRunner(Config{Root: t.TempDir(), Stdout: io.Discard, Stderr: io.Discard}); !errors.Is(err, want) {
		t.Fatalf("watcher creation error = %v", err)
	}
	newSourceWatcher = originalWatcher

	root := t.TempDir()
	watcher := &Watcher{root: root, ignore: newIgnoreMatcher(root, nil)}
	walkDirectory = func(root string, callback fs.WalkDirFunc) error {
		return callback(root, nil, os.ErrNotExist)
	}
	if _, err := watcher.scan(); err != nil {
		t.Fatalf("removed walk entry = %v", err)
	}
	walkDirectory = func(root string, callback fs.WalkDirFunc) error {
		return callback(root, nil, want)
	}
	if _, err := watcher.scan(); !errors.Is(err, want) {
		t.Fatalf("walk error = %v", err)
	}
}
