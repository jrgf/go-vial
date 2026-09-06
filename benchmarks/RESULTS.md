# Performance pass, September 5, 2026

Vial's concurrent dispatch takes about 41% less time and makes half as many allocations. The HTTP comparison does **not** establish a speed advantage over Fiber.

Baseline: `d295a80`, with the new dispatch benchmark added. Modified version: this working tree. Machine: Apple M4 Pro, 48 GiB RAM, macOS arm64, Go 1.26.6. Six samples per case; comparisons use `benchstat` version `v0.0.0-20260825160852-19be9d8e6c70`. No profiling was enabled for the final measurements.

| Concurrent dispatch, 14 workers | Baseline | Modified |
| --- | ---: | ---: |
| Vial ns/op | 642.3 | 380.9 |
| Vial B/op | 1,192 | 640 |
| Vial allocations/op | 14 | 7 |
| Plain net/http ns/op | 161.8 | 161.9 |

The Vial timing difference is significant, `p=0.002`; the standard-library control is unchanged. These are aggregate operation costs, not individual request latency. [Dispatch results](../.vial/performance/2026-09-05/vial-final-stats-dispatch.txt).

The changes remove the build lock after successful initialization, attach request state with one request copy, and allocate values only when written. Applications without global middleware skip the preliminary metadata lookup. String responses use native string writes, and optional response capabilities avoid interface-wrapper allocations. Request contexts are not pooled.

Existing text, JSON, binding, parameter, and route-count benchmarks show approximately 9–21% lower ns/op. Large-body results are mixed: 64 KB JSON was 4.8% slower in an alternating mixed-workload check, while a subsequent isolated comparison found no significant difference. Multipart timing was unchanged. Do not advertise a uniform speedup. [Full results](../.vial/performance/2026-09-05/vial-final-stats-http.txt), [mixed-workload check](../.vial/performance/2026-09-05/vial-paired-stats.txt), [isolated check](../.vial/performance/2026-09-05/vial-large-stats.txt).

| HTTP/1.1 loopback, 8 workers, µs/op | Vial | Fiber 3.5.0 |
| --- | ---: | ---: |
| Text | 9.52 | 9.94 |
| JSON | 9.67 | 9.73 |
| Parameter | 9.76 | 9.94 |

None of these timing differences is statistically significant. Framework order alternated across six rounds. The client and server share a process, and all responses are checked. Fiber still allocates less: the text case uses 57 allocations/op versus Vial's 82, including client allocations. [Comparison results](../.vial/performance/2026-09-05/vial-final-stats-fiber.txt).

`make check` passed, covering coverage thresholds, race detection, vet, standalone examples, and the CLI build. The comparison module also passed its race tests. Added checks cover concurrent value initialization, context replacement and cancellation, response hooks, and partial string-write failures. These checks do not replace production load testing.

For the next pass, use JSON APIs with auth, validation, and realistic payload sizes as the default target. Compare throughput and p95/p99 latency with an external load generator, fixed resources, and equal behavior. Include overload, malformed requests, cancellation, and shutdown. Keep body limits, safe request lifetimes, and net/http compatibility intact.

[Reproduction commands and methodology](README.md). Raw samples and verification logs are saved locally in `.vial/performance/2026-09-05/`, which existing repository rules exclude from Git.
