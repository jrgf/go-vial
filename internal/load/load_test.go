package load

import (
	"bytes"
	"context"
	"errors"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("write failed") }

func TestRun(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)

	result, err := Run(context.Background(), Config{
		URL:      server.URL,
		Workers:  4,
		Duration: 20 * time.Millisecond,
		Timeout:  100 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Requests == 0 || result.Errors != 0 || result.StatusCodes[http.StatusNoContent] != result.Requests {
		t.Fatalf("result=%#v", result)
	}
	if result.LatencyP50 <= 0 || result.LatencyP95 < result.LatencyP50 || result.LatencyP99 < result.LatencyP95 {
		t.Fatalf("latencies=%s/%s/%s", result.LatencyP50, result.LatencyP95, result.LatencyP99)
	}
}

func TestRunTransportErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	target := server.URL
	server.Close()

	result, err := Run(context.Background(), Config{
		URL:      target,
		Workers:  1,
		Duration: 5 * time.Millisecond,
		Timeout:  20 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Requests == 0 || result.Errors != result.Requests || len(result.StatusCodes) != 0 {
		t.Fatalf("result=%#v", result)
	}
}

func TestWorkerStartDelaySupportsMaximumConcurrency(t *testing.T) {
	const workers = 10_000
	first := workerStartDelay(0, workers, 30*time.Second)
	last := workerStartDelay(workers-1, workers, 30*time.Second)
	if first != 0 || last >= maxWorkerRamp {
		t.Fatalf("worker delays=%s/%s", first, last)
	}
}

func TestThresholdsAndSummary(t *testing.T) {
	result := Result{
		Elapsed:     time.Second,
		Requests:    100,
		Errors:      2,
		StatusCodes: map[int]uint64{http.StatusOK: 93, http.StatusInternalServerError: 5},
		LatencyP50:  10 * time.Millisecond,
		LatencyP95:  40 * time.Millisecond,
		LatencyP99:  80 * time.Millisecond,
	}
	if result.Failures() != 7 || result.ErrorRate() != 7 || result.Throughput() != 100 {
		t.Fatalf("failures=%d error rate=%f throughput=%f", result.Failures(), result.ErrorRate(), result.Throughput())
	}
	if err := Check(result, Thresholds{MaxErrorRate: 7, MaxP95: 40 * time.Millisecond}); err != nil {
		t.Fatal(err)
	}
	if err := Check(result, Thresholds{MaxErrorRate: 6, MaxP95: 30 * time.Millisecond}); err == nil ||
		!strings.Contains(err.Error(), "error rate") || !strings.Contains(err.Error(), "p95") {
		t.Fatalf("threshold error=%v", err)
	}

	var output bytes.Buffer
	if err := WriteSummary(&output, result); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{
		"Requests: 100",
		"Throughput: 100.00 req/s",
		"Errors: transport=2 total=7 (7.00%)",
		"p50=10ms p95=40ms p99=80ms",
		"Status: 200=93 500=5",
	} {
		if !strings.Contains(output.String(), value) {
			t.Errorf("summary does not contain %q: %s", value, output.String())
		}
	}
}

func TestValidation(t *testing.T) {
	valid := Config{URL: "http://example.com", Workers: 1, Duration: time.Second, Timeout: time.Second}
	tests := []Config{
		{},
		{URL: "file:///tmp/test", Workers: 1, Duration: time.Second, Timeout: time.Second},
		{URL: valid.URL, Workers: 0, Duration: valid.Duration, Timeout: valid.Timeout},
		{URL: valid.URL, Workers: 10_001, Duration: valid.Duration, Timeout: valid.Timeout},
		{URL: valid.URL, Workers: 1, Timeout: valid.Timeout},
		{URL: valid.URL, Workers: 1, Duration: valid.Duration},
	}
	for _, config := range tests {
		if _, err := Run(context.Background(), config); err == nil {
			t.Fatalf("expected configuration error for %#v", config)
		}
	}
	for _, thresholds := range []Thresholds{
		{MaxErrorRate: -2},
		{MaxErrorRate: 101},
		{MaxErrorRate: math.NaN()},
		{MaxErrorRate: -1, MaxP95: -1},
	} {
		if err := Check(Result{}, thresholds); err == nil {
			t.Fatalf("expected threshold error for %#v", thresholds)
		}
	}
}

func TestCoverageEdgeCases(t *testing.T) {
	contextValue, cancel := context.WithCancel(context.Background())
	cancel()
	result, err := Run(contextValue, Config{
		URL: "http://example.com", Workers: 2, Duration: 100 * time.Millisecond, Timeout: time.Second,
	})
	if !errors.Is(err, context.Canceled) || result.Requests != 0 {
		t.Fatalf("cancelled run = %#v, %v", result, err)
	}

	empty := Result{}
	if empty.ErrorRate() != 0 || empty.Throughput() != 0 {
		t.Fatalf("empty rates = %f/%f", empty.ErrorRate(), empty.Throughput())
	}
	if err := WriteSummary(io.Discard, empty); err != nil {
		t.Fatal(err)
	}
	if err := WriteSummary(failingWriter{}, empty); err == nil {
		t.Fatal("expected summary write error")
	}

	measurements := newMetrics(20*time.Second + time.Nanosecond)
	measureRequest(context.Background(), http.DefaultClient, "://", measurements)
	measurements.record(1001, false, time.Hour)
	if got := measurements.result(time.Second); got.Requests != 2 || got.Errors != 1 {
		t.Fatalf("metrics result = %#v", got)
	}
	if got := newMetrics(time.Nanosecond); got.bucketWidth != time.Microsecond {
		t.Fatalf("minimum bucket width = %s", got.bucketWidth)
	}
	if got := newMetrics(time.Second).quantile(1, 50); got <= time.Second {
		t.Fatalf("empty quantile = %s", got)
	}
}
