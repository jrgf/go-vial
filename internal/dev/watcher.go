package dev

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

const defaultScanInterval = 100 * time.Millisecond

var walkDirectory = filepath.WalkDir

// Change identifies a source path that may require a rebuild.
type Change struct {
	Path string
}

type fileFingerprint struct {
	size       int64
	modifiedAt int64
	mode       fs.FileMode
}

// Watcher polls project files recursively. Polling uses only the standard
// library and works on filesystems without native change notifications.
type Watcher struct {
	root     string
	ignore   *ignoreMatcher
	interval time.Duration
	changes  chan Change
	errors   chan error
	done     chan struct{}
	stopped  chan struct{}
	close    sync.Once
	snapshot map[string]fileFingerprint
	patterns []string
}

func NewWatcher(root string, excludes []string, patterns ...string) (*Watcher, error) {
	absoluteRoot, err := makeAbsolute(root)
	if err != nil {
		return nil, err
	}

	watcher := &Watcher{
		root:     absoluteRoot,
		ignore:   newIgnoreMatcher(absoluteRoot, excludes),
		interval: defaultScanInterval,
		changes:  make(chan Change, 32),
		errors:   make(chan error, 8),
		done:     make(chan struct{}),
		stopped:  make(chan struct{}),
	}
	for _, pattern := range patterns {
		pattern = strings.TrimPrefix(strings.TrimSpace(pattern), "./")
		if _, err := path.Match(pattern, ""); err != nil || pattern == "" || strings.HasPrefix(pattern, "/") || slices.Contains(strings.Split(pattern, "/"), "..") {
			return nil, fmt.Errorf("invalid watch pattern %q: use a relative path or filename pattern", pattern)
		}
		watcher.patterns = append(watcher.patterns, pattern)
	}

	watcher.snapshot, err = watcher.scan()
	if err != nil {
		return nil, err
	}

	go watcher.loop()
	return watcher, nil
}

func (watcher *Watcher) Changes() <-chan Change {
	return watcher.changes
}

func (watcher *Watcher) Errors() <-chan error {
	return watcher.errors
}

func (watcher *Watcher) Close() error {
	watcher.close.Do(func() {
		close(watcher.done)
		<-watcher.stopped
	})
	return nil
}

func (watcher *Watcher) loop() {
	defer close(watcher.stopped)
	defer close(watcher.changes)
	defer close(watcher.errors)

	ticker := time.NewTicker(watcher.interval)
	defer ticker.Stop()

	for {
		select {
		case <-watcher.done:
			return
		case <-ticker.C:
			watcher.scanForChanges()
		}
	}
}

func (watcher *Watcher) scanForChanges() {
	current, err := watcher.scan()
	if err != nil {
		watcher.reportError(err)
		return
	}

	for path, fingerprint := range current {
		previous, existed := watcher.snapshot[path]
		if !existed || previous != fingerprint {
			watcher.reportChange(path)
		}
	}
	for path := range watcher.snapshot {
		if _, stillExists := current[path]; !stillExists {
			watcher.reportChange(path)
		}
	}
	watcher.snapshot = current
}

func (watcher *Watcher) scan() (map[string]fileFingerprint, error) {
	snapshot := make(map[string]fileFingerprint)
	err := walkDirectory(watcher.root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			if errors.Is(walkErr, os.ErrNotExist) {
				return nil
			}
			return walkErr
		}
		if entry.IsDir() {
			if watcher.ignore.Match(path) {
				return filepath.SkipDir
			}
			return nil
		}
		if watcher.ignore.Match(path) || !watcher.relevant(path) {
			return nil
		}

		info, err := entry.Info()
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			return err
		}
		snapshot[path] = fileFingerprint{
			size:       info.Size(),
			modifiedAt: info.ModTime().UnixNano(),
			mode:       info.Mode(),
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return snapshot, nil
}

func (watcher *Watcher) relevant(filename string) bool {
	if isRelevantSource(filename) {
		return true
	}
	if len(watcher.patterns) == 0 {
		return false
	}
	relative, err := filepath.Rel(watcher.root, filename)
	if err != nil {
		return false
	}
	relative = filepath.ToSlash(relative)
	for _, pattern := range watcher.patterns {
		name := relative
		if !strings.Contains(pattern, "/") {
			name = filepath.Base(filename)
		}
		if matched, _ := path.Match(pattern, name); matched {
			return true
		}
	}
	return false
}

func (watcher *Watcher) reportChange(path string) {
	select {
	case watcher.changes <- Change{Path: path}:
	default:
		// One queued change is enough to schedule a rebuild. Dropping excess burst
		// notifications keeps scans non-blocking while a build is in progress.
	}
}

func (watcher *Watcher) reportError(err error) {
	select {
	case watcher.errors <- err:
	default:
	}
}

func isRelevantSource(path string) bool {
	base := filepath.Base(path)
	if base == "go.mod" || base == "go.sum" || base == "go.work" || base == "go.work.sum" {
		return true
	}
	return strings.EqualFold(filepath.Ext(base), ".go")
}
