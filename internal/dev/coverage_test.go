package dev

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("write failed") }

type nthFailWriter struct {
	buffer bytes.Buffer
	mu     sync.Mutex
	failAt int
	calls  int
}

type lockedBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (buffer *lockedBuffer) Write(value []byte) (int, error) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buffer.Write(value)
}

func (buffer *lockedBuffer) String() string {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buffer.String()
}

func (buffer *lockedBuffer) Reset() {
	buffer.mu.Lock()
	buffer.buffer.Reset()
	buffer.mu.Unlock()
}

func (buffer *lockedBuffer) waitFor(text string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if strings.Contains(buffer.String(), text) {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return strings.Contains(buffer.String(), text)
}

func (writer *nthFailWriter) Write(value []byte) (int, error) {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	writer.calls++
	if writer.calls == writer.failAt {
		return 0, errors.New("write failed")
	}
	return writer.buffer.Write(value)
}

func writeTestModule(t *testing.T, source string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/coverage\n\ngo 1.23.0\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte(source), 0o644); err != nil {
		t.Fatalf("write main.go: %v", err)
	}
	return root
}

func TestConfigBuilderAndResolverEdges(t *testing.T) {
	defaults, err := (Config{}).withDefaults()
	if err != nil {
		t.Fatalf("default config: %v", err)
	}
	if defaults.Root == "" || defaults.Target != "." || defaults.Stdin == nil || defaults.Stdout == nil || defaults.Stderr == nil {
		t.Fatalf("defaults = %#v", defaults)
	}
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := (Config{Root: file}).withDefaults(); err == nil {
		t.Fatal("file project root succeeded")
	}
	if _, err := (Config{Root: filepath.Join(t.TempDir(), "missing")}).withDefaults(); err == nil {
		t.Fatal("missing project root succeeded")
	}

	if directory, target, err := ResolvePackage("", ""); err != nil || directory == "" || target == "" {
		t.Fatalf("default package = %q %q, %v", directory, target, err)
	}
	root := writeTestModule(t, "package main\nfunc main() {}\n")
	if directory, target, err := ResolvePackage(root, "missing/import/path"); err != nil || directory != root || target != "missing/import/path" {
		t.Fatalf("missing target = %q %q, %v", directory, target, err)
	}
	if directory, target, err := ResolvePackage(root, "main.go"); err != nil || directory != root || target != "main.go" {
		t.Fatalf("file target = %q %q, %v", directory, target, err)
	}
	nested := filepath.Join(root, "cmd", "app")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if directory, target, err := ResolvePackage(root, nested); err != nil || directory != root || !strings.HasSuffix(target, filepath.Join("cmd", "app")) {
		t.Fatalf("nested target = %q %q, %v", directory, target, err)
	}

	blocked := writeTestModule(t, "package main\nfunc main() {}\n")
	if err := os.WriteFile(filepath.Join(blocked, ".vial"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := NewBuilder(Config{Root: blocked, Target: ".", Stdout: io.Discard, Stderr: io.Discard, Env: os.Environ()}).Build(context.Background()); err == nil {
		t.Fatal("blocked build directory succeeded")
	}
	if _, err := NewBuilder(Config{Root: root, Target: ".", Stdout: failingWriter{}, Stderr: io.Discard, Env: os.Environ()}).Build(context.Background()); err == nil {
		t.Fatal("failed build-status writer succeeded")
	}
	writer := &nthFailWriter{failAt: 2}
	if _, err := NewBuilder(Config{Root: root, Target: ".", Stdout: writer, Stderr: io.Discard, Env: os.Environ()}).Build(context.Background()); err == nil {
		t.Fatal("failed build-success writer succeeded")
	}
}

func TestWatcherEdgeBranches(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing")
	missingWatcher, err := NewWatcher(missing, nil)
	if err != nil {
		t.Fatalf("watch missing root: %v", err)
	}
	if err := missingWatcher.Close(); err != nil {
		t.Fatalf("close missing-root watcher: %v", err)
	}
	root := t.TempDir()
	path := filepath.Join(root, "removed.go")
	if err := os.WriteFile(path, []byte("package removed"), 0o644); err != nil {
		t.Fatal(err)
	}
	watcher, err := NewWatcher(root, nil)
	if err != nil {
		t.Fatalf("new watcher: %v", err)
	}
	defer func() {
		if closeErr := watcher.Close(); closeErr != nil {
			t.Errorf("close watcher: %v", closeErr)
		}
	}()
	watcher.reportChange("first")
	for range cap(watcher.changes) {
		watcher.reportChange("full")
	}
	watcher.reportError(errors.New("first"))
	for range cap(watcher.errors) {
		watcher.reportError(errors.New("full"))
	}
	watcher.snapshot = map[string]fileFingerprint{path: watcher.snapshot[path]}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	watcher.scanForChanges()

	broken := &Watcher{
		root:     "\x00",
		ignore:   newIgnoreMatcher("\x00", nil),
		changes:  make(chan Change, 1),
		errors:   make(chan error, 1),
		snapshot: make(map[string]fileFingerprint),
	}
	broken.scanForChanges()
	select {
	case <-broken.errors:
	default:
		t.Fatal("scan error was not reported")
	}
}

func TestProcessLifecycleEdges(t *testing.T) {
	if _, err := StartProcess(Config{Root: t.TempDir(), Stdout: io.Discard, Stderr: io.Discard, Env: os.Environ()}, filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("missing executable started")
	}
	command := &exec.Cmd{}
	if err := interruptCommand(command); err != nil {
		t.Fatalf("interrupt without process: %v", err)
	}
	if err := killCommand(command); err != nil {
		t.Fatalf("kill without process: %v", err)
	}
	if runtime.GOOS == "windows" {
		t.Skip("shell process assertions are Unix-specific")
	}

	root := t.TempDir()
	failed, err := StartProcess(Config{Root: root, AppArgs: []string{"-c", "exit 7"}, Stdout: io.Discard, Stderr: io.Discard, Env: os.Environ()}, "/bin/sh")
	if err != nil {
		t.Fatalf("start failing process: %v", err)
	}
	<-failed.Done()
	if failed.Err() == nil {
		t.Fatal("failed process had no error")
	}
	if err := failed.Stop(time.Second); err != nil {
		t.Fatalf("stop completed process: %v", err)
	}

	stubborn, err := StartProcess(Config{Root: root, AppArgs: []string{"-c", `trap '' INT; sleep 5`}, Stdout: io.Discard, Stderr: io.Discard, Env: os.Environ()}, "/bin/sh")
	if err != nil {
		t.Fatalf("start stubborn process: %v", err)
	}
	time.Sleep(20 * time.Millisecond)
	if err := stubborn.Stop(20 * time.Millisecond); err != nil {
		t.Fatalf("force stop process: %v", err)
	}
}

func manualRunner(t *testing.T, source string, stdout, stderr io.Writer) (*Runner, chan Change, chan error) {
	t.Helper()
	root := writeTestModule(t, source)
	runner, err := NewRunner(Config{
		Root: root, Debounce: 5 * time.Millisecond, RestartTimeout: time.Second,
		Verbose: true, Stdout: stdout, Stderr: stderr, Env: os.Environ(),
	})
	if err != nil {
		t.Fatalf("new runner: %v", err)
	}
	if err := runner.watcher.Close(); err != nil {
		t.Fatalf("close automatic watcher: %v", err)
	}
	changes := make(chan Change, 8)
	errorsChannel := make(chan error, 2)
	stopped := make(chan struct{})
	close(stopped)
	runner.watcher = &Watcher{changes: changes, errors: errorsChannel, done: make(chan struct{}), stopped: stopped}
	return runner, changes, errorsChannel
}

func TestRunnerOutputWatcherDebounceAndExitBranches(t *testing.T) {
	runner, _, _ := manualRunner(t, "package main\nfunc main() {}\n", failingWriter{}, io.Discard)
	if err := runner.Run(context.Background()); err == nil {
		t.Fatal("runner with failed initial output succeeded")
	}

	writer := &nthFailWriter{failAt: 2}
	runner, _, _ = manualRunner(t, "package main\nfunc main() {}\n", writer, failingWriter{})
	if err := runner.Run(context.Background()); err == nil {
		t.Fatal("runner with failed builder output succeeded")
	}

	var output lockedBuffer
	runner, changes, watcherErrors := manualRunner(t, "package main\nimport \"time\"\nfunc main(){ time.Sleep(150*time.Millisecond) }\n", &output, &output)
	contextValue, cancel := context.WithCancel(context.Background())
	go func() {
		defer cancel()
		defer close(changes)
		if !output.waitFor("application started", 10*time.Second) {
			return
		}
		watcherErrors <- errors.New("watch failed")
		changes <- Change{Path: "first.go"}
		changes <- Change{Path: "second.go"}
		if !output.waitFor("source change detected", 10*time.Second) {
			return
		}
		output.waitFor("application exited", 10*time.Second)
	}()
	if err := runner.Run(contextValue); err != nil {
		t.Fatalf("run controlled events: %v", err)
	}
	if text := output.String(); !strings.Contains(text, "watcher error") || !strings.Contains(text, "change detected") || !strings.Contains(text, "source change detected") || !strings.Contains(text, "application exited") {
		t.Fatalf("runner output missing events:\n%s", text)
	}

	output.Reset()
	runner, changes, _ = manualRunner(t, "package main\nimport \"os\"\nfunc main(){ os.Exit(2) }\n", &output, &output)
	go func() {
		output.waitFor("application exited:", 10*time.Second)
		close(changes)
	}()
	if err := runner.Run(context.Background()); err != nil {
		t.Fatalf("run failed child: %v", err)
	}
	if !strings.Contains(output.String(), "application exited:") {
		t.Fatalf("missing failed child output:\n%s", output.String())
	}
}

func TestRunnerHelpersAndWriteErrors(t *testing.T) {
	runner := &Runner{config: Config{Stdout: io.Discard, Stderr: io.Discard}}
	if runner.processDone() != nil || runner.stopCurrent() != nil {
		t.Fatal("empty runner helpers failed")
	}

	root := writeTestModule(t, "package main\nfunc main() {}\n")
	badOutput := &nthFailWriter{failAt: 1}
	runner = &Runner{
		config:  Config{Root: root, Target: ".", Stdout: io.Discard, Stderr: badOutput, Env: os.Environ()},
		builder: NewBuilder(Config{Root: root, Target: ".", Stdout: failingWriter{}, Stderr: io.Discard, Env: os.Environ()}),
	}
	if err := runner.rebuildAndSwap(context.Background()); err == nil {
		t.Fatal("rebuild output failure was ignored")
	}
}

func TestRunnerEventWriteAndReplacementFailures(t *testing.T) {
	longRunning := "package main\nimport \"time\"\nfunc main(){ time.Sleep(time.Second) }\n"

	verboseWriter := &nthFailWriter{failAt: 5}
	runner, changes, _ := manualRunner(t, longRunning, verboseWriter, io.Discard)
	changes <- Change{Path: "changed.go"}
	if err := runner.Run(context.Background()); err == nil {
		t.Fatal("verbose change write failure was ignored")
	}

	debounceWriter := &nthFailWriter{failAt: 6}
	runner, changes, _ = manualRunner(t, longRunning, debounceWriter, io.Discard)
	changes <- Change{Path: "changed.go"}
	if err := runner.Run(context.Background()); err == nil {
		t.Fatal("debounce write failure was ignored")
	}

	runner, _, watcherErrors := manualRunner(t, longRunning, io.Discard, failingWriter{})
	watcherErrors <- errors.New("watch failed")
	if err := runner.Run(context.Background()); err == nil {
		t.Fatal("watcher error write failure was ignored")
	}

	successExitWriter := &nthFailWriter{failAt: 5}
	runner, _, _ = manualRunner(t, "package main\nfunc main() {}\n", successExitWriter, io.Discard)
	if err := runner.Run(context.Background()); err == nil {
		t.Fatal("successful process exit write failure was ignored")
	}

	runner, _, _ = manualRunner(t, "package main\nimport \"os\"\nfunc main(){ os.Exit(2) }\n", io.Discard, failingWriter{})
	if err := runner.Run(context.Background()); err == nil {
		t.Fatal("failed process exit write failure was ignored")
	}

	var rebuildOutput lockedBuffer
	runner, changes, _ = manualRunner(t, longRunning, &rebuildOutput, failingWriter{})
	contextValue, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	go func() {
		if !rebuildOutput.waitFor("application started", 10*time.Second) {
			return
		}
		_ = os.WriteFile(filepath.Join(runner.config.Root, "main.go"), []byte("package main\nfunc main( {\n"), 0o644)
		changes <- Change{Path: "main.go"}
	}()
	if err := runner.Run(contextValue); err == nil {
		t.Fatal("debounced rebuild output failure was ignored")
	}

	root := writeTestModule(t, "package main\nfunc main() {}\n")
	stuck := &Process{command: &exec.Cmd{}, done: make(chan struct{})}
	runner = &Runner{
		config:  Config{Root: root, Target: ".", RestartTimeout: time.Millisecond, Stdout: io.Discard, Stderr: io.Discard, Env: os.Environ()},
		builder: NewBuilder(Config{Root: root, Target: ".", Stdout: io.Discard, Stderr: io.Discard, Env: os.Environ()}),
		process: stuck,
	}
	if err := runner.rebuildAndSwap(context.Background()); err != nil {
		t.Fatalf("replacement stop failure logging: %v", err)
	}

	stuck = &Process{command: &exec.Cmd{}, done: make(chan struct{})}
	runner = &Runner{config: Config{RestartTimeout: time.Millisecond, Stdout: failingWriter{}, Stderr: failingWriter{}}, process: stuck, processBinary: filepath.Join(root, "old")}
	if err := runner.stopCurrent(); err == nil {
		t.Fatal("stopCurrent failures were ignored")
	}

	missingRoot := filepath.Join(t.TempDir(), "missing")
	runner = &Runner{
		config:  Config{Root: missingRoot, Target: ".", Stdout: io.Discard, Stderr: io.Discard, Env: os.Environ()},
		builder: NewBuilder(Config{Root: root, Target: ".", Stdout: io.Discard, Stderr: io.Discard, Env: os.Environ()}),
	}
	if err := runner.rebuildAndSwap(context.Background()); err != nil {
		t.Fatalf("start failure logging: %v", err)
	}
	runner.processBinary = filepath.Join(root, "old")
	if err := runner.rebuildAndSwap(context.Background()); err != nil {
		t.Fatalf("restore failure logging: %v", err)
	}

	file := filepath.Join(t.TempDir(), "root-file")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := NewRunner(Config{Root: file}); err == nil {
		t.Fatal("NewRunner accepted a file root")
	}
}
