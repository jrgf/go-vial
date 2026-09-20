package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestInspectionTimesOutOldApplication(t *testing.T) {
	dir := t.TempDir()
	for name, data := range map[string]string{
		"go.mod":  "module oldapp\n\ngo 1.23\n",
		"main.go": "package main; import \"time\"; func main() { time.Sleep(2*time.Second) }",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	// The old executable exits on its own so the regression also fails promptly
	// before the CLI has a timeout implementation.
	old := inspectionTimeout
	inspectionTimeout = 100 * time.Millisecond
	defer func() { inspectionTimeout = old }()
	_, err := inspectApplication(dir, nil)
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("error=%v", err)
	}
}
