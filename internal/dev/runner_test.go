package dev

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRunnerDoesNotStartProcessWhenBuildFails(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/testapp\n\ngo 1.23.0\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\nfunc main( {\n"), 0o644); err != nil {
		t.Fatalf("write main.go: %v", err)
	}

	var stderr bytes.Buffer
	runner, err := NewRunner(Config{Root: root, Stdout: io.Discard, Stderr: &stderr})
	if err != nil {
		t.Fatalf("new runner: %v", err)
	}
	t.Cleanup(func() {
		if err := runner.watcher.Close(); err != nil {
			t.Errorf("close watcher: %v", err)
		}
	})

	if err := runner.rebuildAndSwap(context.Background()); err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	if runner.process != nil {
		t.Fatal("expected failed build not to start a process")
	}
	if !strings.Contains(stderr.String(), "build failed; last successful application remains running") {
		t.Fatalf("missing build failure log:\n%s", stderr.String())
	}
}

func TestRunnerRebuildsAndStopsApplication(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/testapp\n\ngo 1.23.0\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}

	source := `package main

import (
	"os"
	"os/signal"
	"strconv"
)

func main() {
	_ = os.WriteFile("started-"+strconv.Itoa(os.Getpid()), nil, 0o644)
	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt)
	<-interrupt
}
`
	mainPath := filepath.Join(root, "main.go")
	if err := os.WriteFile(mainPath, []byte(source), 0o644); err != nil {
		t.Fatalf("write main.go: %v", err)
	}

	output, err := os.Create(filepath.Join(root, "runner.log"))
	if err != nil {
		t.Fatalf("create runner log: %v", err)
	}

	runner, err := NewRunner(Config{
		Root:           root,
		Debounce:       10 * time.Millisecond,
		RestartTimeout: 3 * time.Second,
		Stdout:         output,
		Stderr:         output,
	})
	if err != nil {
		t.Fatalf("new runner: %v", err)
	}

	contextValue, cancel := context.WithCancel(context.Background())
	verification := make(chan error, 1)
	go func() {
		defer cancel()
		firstProcess, err := waitForNewMarker(root, "", 10*time.Second)
		if err == nil {
			err = os.WriteFile(mainPath, []byte(source+"\n// rebuild\n"), 0o644)
		}
		if err == nil {
			_, err = waitForNewMarker(root, firstProcess, 10*time.Second)
		}
		verification <- err
	}()

	runErr := runner.Run(contextValue)
	verificationErr := <-verification
	if err := output.Close(); err != nil {
		t.Fatalf("close runner log: %v", err)
	}
	logs, err := os.ReadFile(output.Name())
	if err != nil {
		t.Fatalf("read runner log: %v", err)
	}
	if verificationErr != nil {
		t.Fatalf("verify rebuild: %v\nlogs:\n%s", verificationErr, logs)
	}
	if runErr != nil {
		t.Fatalf("run: %v", runErr)
	}
	if !strings.Contains(string(logs), "source change detected; rebuilding") {
		t.Fatalf("missing rebuild log:\n%s", logs)
	}
	entries, err := os.ReadDir(filepath.Join(root, ".vial", "bin"))
	if err != nil {
		t.Fatalf("read build directory: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected runner to remove binaries, found %d", len(entries))
	}
}

func waitForNewMarker(root, previous string, timeout time.Duration) (string, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		entries, err := os.ReadDir(root)
		if err != nil {
			return "", err
		}
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), "started-") && entry.Name() != previous {
				return entry.Name(), nil
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	return "", fmt.Errorf("timed out waiting for a new process marker in %s", root)
}
