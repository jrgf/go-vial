package middleware

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jrgf/go-vial"
)

var httpDurationBuckets = [...]float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}
var metricLabelReplacer = strings.NewReplacer("\\", "\\\\", "\n", "\\n", "\"", "\\\"")

// HTTPMetrics records bounded, route-based HTTP metrics. Its zero value is
// ready to use and must not be copied after first use.
type HTTPMetrics struct {
	mu     sync.Mutex
	series map[httpMetricKey]*httpMetricSeries
	active atomic.Int64
}

type httpMetricKey struct {
	method string
	route  string
	status int
}

type httpMetricSeries struct {
	requests   uint64
	duration   float64
	bucketHits [len(httpDurationBuckets)]uint64
}

type httpMetricSnapshot struct {
	key    httpMetricKey
	series httpMetricSeries
}

// Middleware records request counts and duration histograms. Labels use route
// patterns rather than request paths to prevent attacker-controlled cardinality.
func (metrics *HTTPMetrics) Middleware() vial.Middleware {
	if metrics == nil {
		panic("vial: HTTP metrics recorder is required")
	}
	return func(next vial.Handler) vial.Handler {
		return func(context *vial.Context) error {
			started := time.Now()
			metrics.active.Add(1)
			if err := context.AfterResponse(func() {
				metrics.active.Add(-1)
				metrics.observe(context, context.Status(), time.Since(started))
			}); err != nil {
				metrics.active.Add(-1)
				return err
			}
			return next(context)
		}
	}
}

// Handler exposes the current snapshot in Prometheus/OpenMetrics text format.
func (metrics *HTTPMetrics) Handler(context *vial.Context) error {
	if metrics == nil {
		panic("vial: HTTP metrics recorder is required")
	}
	if context.Committed() {
		return errors.New("vial: response already committed")
	}

	snapshot := metrics.snapshot()
	var body strings.Builder
	body.WriteString("# HELP vial_http_requests_total Completed HTTP requests.\n")
	body.WriteString("# TYPE vial_http_requests_total counter\n")
	for _, item := range snapshot {
		fmt.Fprintf(&body, "vial_http_requests_total%s %d\n", metricLabels(item.key), item.series.requests)
	}
	body.WriteString("# HELP vial_http_request_duration_seconds Request handler duration.\n")
	body.WriteString("# TYPE vial_http_request_duration_seconds histogram\n")
	for _, item := range snapshot {
		labels := metricLabelsWithoutClose(item.key)
		for index, upperBound := range httpDurationBuckets {
			fmt.Fprintf(
				&body,
				"vial_http_request_duration_seconds_bucket%s,le=\"%s\"} %d\n",
				labels,
				strconv.FormatFloat(upperBound, 'g', -1, 64),
				item.series.bucketHits[index],
			)
		}
		fmt.Fprintf(&body, "vial_http_request_duration_seconds_bucket%s,le=\"+Inf\"} %d\n", labels, item.series.requests)
		fmt.Fprintf(&body, "vial_http_request_duration_seconds_sum%s %s\n", metricLabels(item.key), strconv.FormatFloat(item.series.duration, 'g', -1, 64))
		fmt.Fprintf(&body, "vial_http_request_duration_seconds_count%s %d\n", metricLabels(item.key), item.series.requests)
	}
	body.WriteString("# HELP vial_http_requests_active HTTP requests currently executing.\n")
	body.WriteString("# TYPE vial_http_requests_active gauge\n")
	fmt.Fprintf(&body, "vial_http_requests_active %d\n# EOF\n", metrics.active.Load())

	context.Response().Header().Set("Content-Type", "application/openmetrics-text; version=1.0.0; charset=utf-8")
	context.Response().WriteHeader(http.StatusOK)
	_, err := io.WriteString(context.Response(), body.String())
	return err
}

func (metrics *HTTPMetrics) observe(context *vial.Context, status int, duration time.Duration) {
	key := httpMetricKey{method: metricMethod(context), route: "unmatched", status: status}
	if route := context.Route(); route != nil {
		key.route = route.Pattern
		if route.Method != "" {
			key.method = route.Method
		}
	}

	metrics.mu.Lock()
	if metrics.series == nil {
		metrics.series = make(map[httpMetricKey]*httpMetricSeries)
	}
	series := metrics.series[key]
	if series == nil {
		series = &httpMetricSeries{}
		metrics.series[key] = series
	}
	series.requests++
	seconds := duration.Seconds()
	series.duration += seconds
	for index, upperBound := range httpDurationBuckets {
		if seconds <= upperBound {
			series.bucketHits[index]++
		}
	}
	metrics.mu.Unlock()
}

func (metrics *HTTPMetrics) snapshot() []httpMetricSnapshot {
	metrics.mu.Lock()
	snapshot := make([]httpMetricSnapshot, 0, len(metrics.series))
	for key, series := range metrics.series {
		snapshot = append(snapshot, httpMetricSnapshot{key: key, series: *series})
	}
	metrics.mu.Unlock()
	sort.Slice(snapshot, func(left, right int) bool {
		if snapshot[left].key.method != snapshot[right].key.method {
			return snapshot[left].key.method < snapshot[right].key.method
		}
		if snapshot[left].key.route != snapshot[right].key.route {
			return snapshot[left].key.route < snapshot[right].key.route
		}
		return snapshot[left].key.status < snapshot[right].key.status
	})
	return snapshot
}

func metricMethod(context *vial.Context) string {
	method := strings.ToUpper(context.Request().Method)
	switch method {
	case http.MethodConnect, http.MethodDelete, http.MethodGet, http.MethodHead,
		http.MethodOptions, http.MethodPatch, http.MethodPost, http.MethodPut, http.MethodTrace:
		return method
	default:
		return "OTHER"
	}
}

func metricLabels(key httpMetricKey) string {
	return metricLabelsWithoutClose(key) + "}"
}

func metricLabelsWithoutClose(key httpMetricKey) string {
	return fmt.Sprintf(
		"{method=\"%s\",route=\"%s\",status=\"%d\"",
		escapeMetricLabel(key.method),
		escapeMetricLabel(key.route),
		key.status,
	)
}

func escapeMetricLabel(value string) string {
	return metricLabelReplacer.Replace(value)
}
