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
