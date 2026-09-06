# Benchmark protocol

Run `make benchmark` on an idle machine with a fixed CPU governor and record the
commit, Go version, operating system, architecture, CPU model, core count, RAM,
and command. The Go benchmark output reports ns/op, B/op, and allocs/op; derive
request throughput from ns/op.

For end-to-end latency and error rate, build an example in release mode and run
`vial load` long enough to reach steady state. Record requests/second, p50, p95,
p99, error rate, CPU utilization, and peak RSS. Use `-cpuprofile` and
`-memprofile` when investigating a change.

Compare at least five samples with `benchstat`; do not gate on a single noisy
run. Treat a regression as actionable when the confidence interval excludes
zero and either allocations increase or latency changes by at least 5%. CI
uploads every raw result so historical route lookup, middleware, binding, error,
and allocation results remain available. Graceful shutdown under active load is
covered by the integration test because a microbenchmark cannot model signals
or connection draining reliably.

## Isolate Vial overhead

`BenchmarkHTTP` includes request creation and response recording. Use
`BenchmarkDispatch` to isolate concurrent dispatch with a request and writer
owned by each worker. Its `net_http` case is a standard-library control.

```sh
go test -run '^$' -bench '^BenchmarkDispatch$' -benchmem -benchtime=500ms -count=6 ./tests/vial
```

With `RunParallel`, ns/op is elapsed benchmark time divided by completed
operations across all workers. It is not an individual request's latency.

## Compare Fiber over HTTP

The isolated `compare` module pins Fiber v3.5.0 and fasthttp v1.73.0. It adds
no dependency to Vial's core module. Run from `benchmarks/compare`:

```sh
go test ./...
go test -run '^$' -bench '^BenchmarkHTTP$' -benchmem -benchtime=300ms -count=6 -cpu=8 .
```

Vial, Fiber, and plain `net/http` serve the same text, JSON, and parameter
responses over TCP with HTTP/1.1 keep-alive. The suite uses the same client,
connection cap, Go scheduler, JSON codec, status, content type, and response
bytes. Every response is checked, and cleanup waits for each server to stop.
Fiber uses its default mutable-context mode, without prefork or an adapter.

These are small-response tests with no application middleware, TLS, database,
or external services. Client and server share one process and CPU budget;
allocations include both sides. They do not establish server capacity, p99
latency, or production reliability. Do not compare these figures directly
with `BenchmarkDispatch` or an in-process Fiber handler benchmark.

Before claiming a production advantage, run the intended application stack
against both frameworks with an external load generator. Compare throughput,
p50/p95/p99, unexpected errors, CPU, and peak RSS at the same connection counts
and resource limits. Include malformed input, canceled requests, overload,
slow clients, and shutdown under traffic. Keep Vial's existing race, fuzz,
body-limit, streaming, and compatibility checks as release gates.
