# Changelog

## 0.17.0

- Removed dead code
- Improved correctness 

## 0.16.0

- Add lifecycle-managed asynchronous HTTP operations with RFC 7240 wait support,
  authorized polling and cancellation, idempotency, progress, and OpenMetrics.
- Add bounded non-durable memory execution and durable PostgreSQL execution with
  renewable leases, multi-replica recovery, and capped exponential retries.

## 0.15.0

- Correct raw-handler middleware, binding precedence and validation, optional
  response-writer interfaces, renderer recovery, strict options, CSRF defaults,
  and bounded testkit requests.
- Stabilize modules, typed request values, trusted proxy and client IP handling,
  route metadata and names, server options, liveness and readiness, and Go
  support.
- Add fuzzing, benchmarks, pinned CI and security checks, reproducible artifacts,
  checksums, SBOM, and provenance.

## 0.12.0

- Complete public API documentation and a fresh external-module compatibility test
- Focused binding, configuration, and CSRF fuzz targets plus concurrent build/request coverage
- Reject malformed CSRF origin hosts and add regression coverage for unsafe incoming request IDs
- `ADDR` overrides for the JSON API and rendered-web examples, matching the hello example

## 0.11.0

- Dedicated `vial load` command with workers, duration, request timeout, thresholds, and bounded latency percentiles
- Ten-thousand-worker concurrency with connection reuse, bounded ramp-up, and progress feedback
- Transport errors, HTTP status counts, throughput, and latency summaries suitable for local and deployed endpoints

## 0.10.0

- Typed request validation after binding with transport-neutral field errors
- Signed double-submit CSRF middleware with strict origin validation and secure cookie defaults
- Server-rendered form example covering validation errors, CSRF tokens, and multipart binding

## 0.9.0

- Isolated `securecookie` integration example with signed cookie sessions
- Minute-scale key-file rotation, explicit persistence, one-time flash messages, and tamper rejection
- Dependency-free Vial module; the optional dependency remains inside the example module
- Nested Go module resolution for `vial dev`, `vial routes`, and `vial doctor`

## 0.8.0

- Buffered `html/template` rendering with named templates and safe error propagation
- Embedded static assets through native `http.FileServerFS` integration
- Runnable server-rendered web example with contextual escaping

## 0.7.0

- Named `App.Go` background tasks with build-time validation and lifecycle-managed cancellation
- Critical and non-critical failure policies with panic recovery and named error propagation
- Deadline-bound task shutdown with runnable heartbeat and in-memory event queue examples

## 0.6.0

- Lifecycle-aware `testkit` server with automatic cleanup and a cookie-aware HTTP client
- JSON and multipart requests with status, text, JSON, and fault response helpers
- Explicit lifecycle shutdown and route metadata checks for deterministic application tests

## 0.5.0

- Deterministic application lifecycle with ordered startup hooks, reverse shutdown hooks, and managed HTTP failure propagation
- Typed configuration from JSON files and environment variables with validation and safe error messages
- HTTP host and port configuration with localhost defaults and IPv6-safe addresses
- `vial doctor` validation without opening a network listener

## 0.4.0

- Named route metadata with build-time duplicate validation and CLI output
- Atomic module registration with middleware, groups, route ownership, and validation
- Transport-neutral application faults with centralized, sanitized HTTP mapping
- Cached path, query, header, cookie, JSON, form, and combined binding with field errors

## 0.3.0

- Framework-owned `404` and `405` errors with native `Allow` headers
- Exact Vial root and trailing-slash routes instead of `ServeMux` catch-alls
- Size-limited multipart form/file handling with temporary-file cleanup
- Restrictive, validated CORS middleware with preflight handling

## 0.2.0

- Read-only route catalog through `App.Routes`
- Route inspection through `vial routes` and `vial routes --json`
- Primitive query and URL-encoded form binding

## 0.1.0

Initial vertical slice of the project:

- HTTP application and context
- Method-aware routing and groups
- Middleware and centralized errors
- Strict JSON binding
- Graceful server shutdown
- Request ID, logging, and panic recovery
- Standard `http.Handler` interoperability
- Automatic development rebuild/restart command
- Last-known-good process retention on build failure
- Cross-platform compile checks and test suite
