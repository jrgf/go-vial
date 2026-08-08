package main

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jrgf/go-vial"
	"github.com/jrgf/go-vial/testkit"
)

type reportJob struct {
	decodeErr   error
	progressErr error
}

func (job reportJob) ID() string                  { return "test" }
func (job reportJob) Name() string                { return "reports.generate" }
func (job reportJob) Metadata() map[string]string { return nil }
func (job reportJob) Decode(destination any) error {
	if job.decodeErr != nil {
		return job.decodeErr
	}
	request := destination.(*generateReportRequest)
	*request = generateReportRequest{Name: "test", DurationMS: 10}
	return nil
}
func (job reportJob) Progress(context.Context, int) error { return job.progressErr }

func TestAsyncReportLifecycle(t *testing.T) {
	server := testkit.Start(t, newApp())

	first := submit(t, server, `{"name":"sales","duration_ms":300}`, "respond-async", "report-1")
	first.RequireStatus(http.StatusAccepted)
	var accepted struct {
		ID        string `json:"id"`
		StatusURL string `json:"status_url"`
	}
	first.Decode(&accepted)
	if accepted.ID == "" || accepted.StatusURL == "" {
		t.Fatalf("unexpected accepted operation: %+v", accepted)
	}

	duplicate := submit(t, server, `{"name":"ignored","duration_ms":300}`, "respond-async", "report-1")
	duplicate.RequireStatus(http.StatusAccepted)
	var duplicateOperation vial.Operation
	duplicate.Decode(&duplicateOperation)
	if duplicateOperation.ID != accepted.ID {
		t.Fatalf("idempotency returned %q, want %q", duplicateOperation.ID, accepted.ID)
	}

	unauthorized := server.Do(server.NewRequest(http.MethodGet, accepted.StatusURL, nil))
	unauthorized.RequireStatus(http.StatusUnauthorized)
	forbiddenRequest := server.NewRequest(http.MethodGet, accepted.StatusURL, nil)
	forbiddenRequest.Header.Set("X-User-ID", "other-user")
	server.Do(forbiddenRequest).RequireStatus(http.StatusNotFound)

	deadline := time.Now().Add(2 * time.Second)
	for {
		request := server.NewRequest(http.MethodGet, accepted.StatusURL, nil)
		request.Header.Set("X-User-ID", "demo-user")
		response := server.Do(request)
		response.RequireStatus(http.StatusOK)
		var operation vial.Operation
		response.Decode(&operation)
		if operation.Status == vial.OperationSucceeded {
			if operation.Progress != 100 {
				t.Fatalf("progress = %d, want 100", operation.Progress)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("operation did not finish: %+v", operation)
		}
		time.Sleep(20 * time.Millisecond)
	}

	metrics := server.Do(server.NewRequest(http.MethodGet, "/metrics/async", nil))
	metrics.RequireStatus(http.StatusOK)
	if !strings.Contains(metrics.Text(), "vial_async_submitted_total 1") {
		t.Fatalf("unexpected metrics:\n%s", metrics.Text())
	}
}

func TestAsyncExampleMain(t *testing.T) {
	t.Setenv("ADDR", "")
	t.Setenv("VIAL_ROUTES_OUTPUT", filepath.Join(t.TempDir(), "routes.json"))
	main()
}

func TestPreferWaitReturnsCompletedResult(t *testing.T) {
	server := testkit.Start(t, newApp())
	response := submit(t, server, `{"name":"fast","duration_ms":100}`, "respond-async, wait=1", "")
	response.RequireStatus(http.StatusCreated)
	var result generatedReport
	response.Decode(&result)
	if result.DownloadURL != "/downloads/fast.csv" {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestReportRequestValidation(t *testing.T) {
	server := testkit.Start(t, newApp())
	tests := []struct {
		name   string
		body   string
		prefer string
		userID string
		status int
	}{
		{"authentication", `{}`, "respond-async", "", http.StatusUnauthorized},
		{"prefer", `{}`, "respond-async, wait=bad", "demo-user", http.StatusBadRequest},
		{"json", `{`, "respond-async", "demo-user", http.StatusBadRequest},
		{"name", `{"name":" "}`, "respond-async", "demo-user", http.StatusBadRequest},
		{"duration", `{"name":"report","duration_ms":99}`, "respond-async", "demo-user", http.StatusBadRequest},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := server.NewRequest(http.MethodPost, "/reports", strings.NewReader(test.body))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Prefer", test.prefer)
			request.Header.Set("X-User-ID", test.userID)
			server.Do(request).RequireStatus(test.status)
		})
	}
}

func TestReportDefaultDuration(t *testing.T) {
	server := testkit.Start(t, newApp())
	response := submit(t, server, `{"name":"default"}`, "respond-async", "")
	response.RequireStatus(http.StatusAccepted)
}

func TestGenerateReportErrorsAndAuthorization(t *testing.T) {
	decodeErr := errors.New("decode")
	if _, err := generateReport(context.Background(), reportJob{decodeErr: decodeErr}); !errors.Is(err, decodeErr) {
		t.Fatalf("decode error = %v", err)
	}

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := generateReport(cancelled, reportJob{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel error = %v", err)
	}

	progressErr := errors.New("progress")
	if _, err := generateReport(context.Background(), reportJob{progressErr: progressErr}); !errors.Is(err, progressErr) {
		t.Fatalf("progress error = %v", err)
	}

}

func submit(t *testing.T, server *testkit.Server, body, prefer, idempotencyKey string) *testkit.Response {
	t.Helper()
	request := server.NewRequest(http.MethodPost, "/reports", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-User-ID", "demo-user")
	request.Header.Set("Prefer", prefer)
	if idempotencyKey != "" {
		request.Header.Set("Idempotency-Key", idempotencyKey)
	}
	return server.Do(request)
}
