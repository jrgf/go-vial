package dev

import (
	"path/filepath"
	"strings"
)

var defaultIgnoredNames = map[string]struct{}{
	".git":         {},
	".vial":        {},
	"vendor":       {},
	"node_modules": {},
	"tmp":          {},
	"dist":         {},
}

type ignoreMatcher struct {
	root   string
	extras []string
}

func newIgnoreMatcher(root string, extras []string) *ignoreMatcher {
	normalized := make([]string, 0, len(extras))
	for _, extra := range extras {
		extra = filepath.Clean(strings.TrimSpace(extra))
		if extra == "" || extra == "." {
			continue
		}
		extra = strings.TrimPrefix(extra, "."+string(filepath.Separator))
		normalized = append(normalized, extra)
	}
	return &ignoreMatcher{root: filepath.Clean(root), extras: normalized}
}

func (matcher *ignoreMatcher) Match(path string) bool {
	relative, err := filepath.Rel(matcher.root, filepath.Clean(path))
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return true
	}
	if relative == "." {
		return false
	}

	parts := strings.Split(relative, string(filepath.Separator))
	for _, part := range parts {
		if _, ignored := defaultIgnoredNames[part]; ignored {
			return true
		}
	}

	for _, extra := range matcher.extras {
		if !strings.ContainsRune(extra, filepath.Separator) {
			for _, part := range parts {
				if part == extra {
					return true
				}
			}
			continue
		}

		if relative == extra || strings.HasPrefix(relative, extra+string(filepath.Separator)) {
			return true
		}
	}
	return false
}
