// Package load runs bounded HTTP load checks for the vial command.
package load

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	latencyBucketCount = 10_000
	maxWorkerRamp      = time.Second
)

// Config controls a load run.
type Config struct {
	URL      string
	Workers  int
	Duration time.Duration
	Timeout  time.Duration
}

// Result is the bounded aggregate from a load run.
type Result struct {
	Elapsed     time.Duration
	Requests    uint64
	Errors      uint64
	StatusCodes map[int]uint64
	LatencyP50  time.Duration
	LatencyP95  time.Duration
	LatencyP99  time.Duration
}

// Thresholds defines optional CI failure gates. MaxErrorRate -1 and MaxP95 0
// disable their respective gates.
type Thresholds struct {
	MaxErrorRate float64
	MaxP95       time.Duration
}

type metrics struct {
	requests    atomic.Uint64
	errors      atomic.Uint64
	statusCodes [1000]atomic.Uint64
	latencies   [latencyBucketCount]atomic.Uint64
	bucketWidth time.Duration
}

// Run sends GET requests until the configured duration expires, then waits for
// in-flight requests to finish.
func Run(contextValue context.Context, config Config) (Result, error) {
	if err := validateConfig(config); err != nil {
		return Result{}, err
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConns = config.Workers
	transport.MaxIdleConnsPerHost = config.Workers
	transport.MaxConnsPerHost = config.Workers
	client := &http.Client{Transport: transport, Timeout: config.Timeout}
	defer transport.CloseIdleConnections()

	started := time.Now()
	deadline := started.Add(config.Duration)
	measurements := newMetrics(config.Timeout)
	var workers sync.WaitGroup
	workers.Add(config.Workers)
	for worker := range config.Workers {
		delay := workerStartDelay(worker, config.Workers, config.Duration)
		go func() {
			defer workers.Done()
			if delay > 0 {
				timer := time.NewTimer(delay)
				defer timer.Stop()
				select {
				case <-contextValue.Done():
					return
				case <-timer.C:
				}
			}
			for time.Now().Before(deadline) && contextValue.Err() == nil {
				measureRequest(contextValue, client, config.URL, measurements)
			}
		}()
	}
	workers.Wait()

	result := measurements.result(time.Since(started))
	if err := contextValue.Err(); err != nil {
		return result, err
	}
	return result, nil
}

func workerStartDelay(worker, workers int, duration time.Duration) time.Duration {
	ramp := min(duration/10, maxWorkerRamp)
	return time.Duration(worker) * ramp / time.Duration(workers)
}

// Check returns an error when a configured threshold is exceeded.
func Check(result Result, thresholds Thresholds) error {
	if math.IsNaN(thresholds.MaxErrorRate) || math.IsInf(thresholds.MaxErrorRate, 0) ||
		(thresholds.MaxErrorRate < 0 && thresholds.MaxErrorRate != -1) || thresholds.MaxErrorRate > 100 {
		return errors.New("max error rate must be between 0 and 100, or -1 to disable")
	}
	if thresholds.MaxP95 < 0 {
		return errors.New("max p95 cannot be negative")
	}

	var failures []string
	if thresholds.MaxErrorRate >= 0 && result.ErrorRate() > thresholds.MaxErrorRate {
		failures = append(failures, fmt.Sprintf("error rate %.2f%% exceeds %.2f%%", result.ErrorRate(), thresholds.MaxErrorRate))
	}
	if thresholds.MaxP95 > 0 && result.LatencyP95 > thresholds.MaxP95 {
		failures = append(failures, fmt.Sprintf("p95 %s exceeds %s", result.LatencyP95, thresholds.MaxP95))
	}
	if len(failures) > 0 {
		return fmt.Errorf("load thresholds failed: %s", strings.Join(failures, "; "))
	}
	return nil
}

// ErrorRate returns transport errors and HTTP responses with status >= 400 as a
// percentage of all requests.
func (result Result) ErrorRate() float64 {
	if result.Requests == 0 {
		return 0
	}
	return float64(result.Failures()) * 100 / float64(result.Requests)
}

// Failures returns transport errors plus HTTP responses with status >= 400.
func (result Result) Failures() uint64 {
	failures := result.Errors
	for status, count := range result.StatusCodes {
		if status >= http.StatusBadRequest {
			failures += count
		}
	}
	return failures
}

// Throughput returns completed requests per second.
func (result Result) Throughput() float64 {
	if result.Elapsed <= 0 {
		return 0
	}
	return float64(result.Requests) / result.Elapsed.Seconds()
}

// WriteSummary writes a fixed-size aggregate report.
func WriteSummary(output io.Writer, result Result) error {
	var summary strings.Builder
	_, _ = fmt.Fprintf(&summary, "Requests: %d\n", result.Requests)
	_, _ = fmt.Fprintf(&summary, "Duration: %s\n", result.Elapsed.Round(time.Millisecond))
	_, _ = fmt.Fprintf(&summary, "Throughput: %.2f req/s\n", result.Throughput())
	_, _ = fmt.Fprintf(
		&summary,
		"Errors: transport=%d total=%d (%.2f%%)\n",
		result.Errors,
		result.Failures(),
		result.ErrorRate(),
	)
	_, _ = fmt.Fprintf(
		&summary,
		"Latency: p50=%s p95=%s p99=%s\n",
		result.LatencyP50.Round(time.Microsecond),
		result.LatencyP95.Round(time.Microsecond),
		result.LatencyP99.Round(time.Microsecond),
	)
	statuses := make([]int, 0, len(result.StatusCodes))
	for status := range result.StatusCodes {
		statuses = append(statuses, status)
	}
	sort.Ints(statuses)
	summary.WriteString("Status:")
	if len(statuses) == 0 {
		summary.WriteString(" none")
	}
	for _, status := range statuses {
		_, _ = fmt.Fprintf(&summary, " %d=%d", status, result.StatusCodes[status])
	}
	summary.WriteByte('\n')
	if _, err := io.WriteString(output, summary.String()); err != nil {
		return fmt.Errorf("write load summary: %w", err)
	}
	return nil
}

func validateConfig(config Config) error {
	parsed, err := url.ParseRequestURI(config.URL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return errors.New("load URL must be an absolute HTTP or HTTPS URL")
	}
	if config.Workers < 1 || config.Workers > 10_000 {
		return errors.New("load workers must be between 1 and 10000")
	}
	if config.Duration <= 0 {
		return errors.New("load duration must be positive")
	}
	if config.Timeout <= 0 {
		return errors.New("load request timeout must be positive")
	}
	return nil
}

func measureRequest(contextValue context.Context, client *http.Client, target string, measurements *metrics) {
	request, err := http.NewRequestWithContext(contextValue, http.MethodGet, target, nil)
	if err != nil {
		measurements.record(0, true, 0)
		return
	}
	request.Header.Set("User-Agent", "vial-load")
	started := time.Now()
	response, err := client.Do(request)
	latency := time.Since(started)
	if err != nil {
		measurements.record(0, true, latency)
		return
	}
	_, readErr := io.Copy(io.Discard, response.Body)
	closeErr := response.Body.Close()
	measurements.record(response.StatusCode, readErr != nil || closeErr != nil, time.Since(started))
}

func newMetrics(timeout time.Duration) *metrics {
	width := timeout / (latencyBucketCount - 1)
	if timeout%(latencyBucketCount-1) != 0 {
		width++
	}
	if width < time.Microsecond {
		width = time.Microsecond
	}
	return &metrics{bucketWidth: width}
}

func (measurements *metrics) record(status int, failed bool, latency time.Duration) {
	measurements.requests.Add(1)
	if failed {
		measurements.errors.Add(1)
	} else if status > 0 && status < len(measurements.statusCodes) {
		measurements.statusCodes[status].Add(1)
	}
	bucket := int(latency / measurements.bucketWidth)
	if bucket >= len(measurements.latencies) {
		bucket = len(measurements.latencies) - 1
	}
	measurements.latencies[bucket].Add(1)
}

func (measurements *metrics) result(elapsed time.Duration) Result {
	result := Result{
		Elapsed:     elapsed,
		Requests:    measurements.requests.Load(),
		Errors:      measurements.errors.Load(),
		StatusCodes: make(map[int]uint64),
	}
	for status := range measurements.statusCodes {
		if count := measurements.statusCodes[status].Load(); count > 0 {
			result.StatusCodes[status] = count
		}
	}
	result.LatencyP50 = measurements.quantile(result.Requests, 50)
	result.LatencyP95 = measurements.quantile(result.Requests, 95)
	result.LatencyP99 = measurements.quantile(result.Requests, 99)
	return result
}

func (measurements *metrics) quantile(total uint64, percentile uint64) time.Duration {
	if total == 0 {
		return 0
	}
	target := (total*percentile + 99) / 100
	var seen uint64
	for index := range measurements.latencies {
		seen += measurements.latencies[index].Load()
		if seen >= target {
			return time.Duration(index+1) * measurements.bucketWidth
		}
	}
	return time.Duration(len(measurements.latencies)) * measurements.bucketWidth
}
