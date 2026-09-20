package vial

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestInspectionRejectsMalformedTargets(t *testing.T) {
	for _, path := range []string{"/a b", "/a\tb", "/a\nb", "/a#fragment", "//host/path"} {
		t.Run(path, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("inspection panicked: %v", r)
				}
			}()
			err := New().writeHTTPInspection(context.Background(), path, filepath.Join(t.TempDir(), "out"))
			if err == nil || !strings.Contains(err.Error(), "invalid path") {
				t.Errorf("error=%v", err)
			}
		})
	}
}
