# Changelog

## 1.0.0-rc.2
Exceptions to the release-candidate API freeze:
- Move `vialgrpc` and `vialws` into separate Go modules so core-only
  applications no longer download grpc-go or coder/websocket. They are tagged
  in lockstep with the core module and require the same core version. Upgrade
  with `go get github.com/jrgf/go-vial/vialgrpc@v1.0.0-rc.2` or
  `go get github.com/jrgf/go-vial/vialws@v1.0.0-rc.2`.
- Add `sqlkit.Dialect` with `PostgreSQL` (default) and `MySQL`. `NewMigrator`
  accepts an optional dialect and serializes migration runs across processes
  with a database session lock.
- Add `Context.ResponseError` to read the handler error or escaping panic in
  `AfterResponse` hooks.

Fixes:
- Let exact and catch-all routes coexist, and invalidate cached route matches
  when middleware rewrites the method or path.
- Keep informational responses from committing the response, and run
  pre-commit hooks once when a wrapped writer is flushed or finalized.
- Persist session mutations on empty and error responses, and keep the
  absolute session lifetime from being extended by writes or key rotation.
- Group IPv6 clients by /64 in the rate limiter, and share one overflow bucket
  at `MaxKeys` instead of rejecting every new key.
- Record unrecovered panics as status 500 in request logs and HTTP metrics,
  and name the OpenMetrics request counter family `vial_http_requests`.
- Release the migration lock on cancellation and failure without returning a
  possibly locked connection to the pool.
- Keep idle HTTP/2 SSE streams and gRPC streams open past server write
  deadlines and HTTP timeouts.
- Time out CLI application inspection, reject malformed inspection paths, and
  normalize native path separators in the `vial dev` watcher.

## 1.0.0-rc.1
- Add `App.URL` for named-route paths with group prefixes, escaped parameters,
  and wildcard validation, using the existing index built during registration.
- Add opt-in RFC 9457 Problem Details through `ProblemDetailsErrorHandler`,
  preserving public error mapping, field errors, and HTTP headers.
- Normalize custom error-header names for case-insensitive HTTP access.
- Mark prerelease versions as prereleases and exclude them from GitHub Latest.
- Consolidate public API tests under `tests/`, retaining package-local tests for
  private internals and updating coverage, fuzzing, and benchmark commands.
- Reject nonportable JSON tag names during OpenAPI inference instead of guessing
  a wire name that differs between supported Go versions.
- Add explicit OpenAPI request and response body schemas for custom JSON types,
  constraints, and examples, with validation and copied configuration buffers.
- Match JSON embedded-field promotion, name conflicts, and `json:",string"`
  in generated schemas; leave custom JSON marshaler output unconstrained.
- Add repeatable `vial dev --watch` patterns for embedded templates, static
  assets, and SQL files, preserving exclusions and debounced rebuilds.
- Remove the application build lock from the request path after a successful
  build, allocate request values lazily, and attach framework state with one
  request-context copy in the common path.
- Avoid redundant route metadata lookup when no application middleware needs
  it, remove interface-wrapper allocations for optional response capabilities,
  and preserve native string writes with response accounting and hooks.
- Add concurrent dispatch benchmarks and an isolated, pinned Fiber comparison
  over HTTP/1.1 with equivalent response checks.

- Add production encrypted cookie sessions with secure defaults, flash values,
  tamper rejection, size limits, and live key rotation.
- Add response pre-commit hooks for safe header persistence.
- Add provider-neutral request identities, challenged authentication guards,
  and application-defined grant checks.
- Add restrictive browser security headers and bounded, trusted-proxy-aware
  in-process rate limiting.
- Add route-bounded HTTP OpenMetrics, standard `net/http` tracing middleware
  integration, final-response hooks, and request/log trace correlation.
- Add `database/sql` transaction and embedded forward-migration helpers with a
  PostgreSQL lifecycle and readiness example.
- Add deterministic OpenAPI 3.1 generation, request and response schemas,
  security metadata, and a cached JSON document endpoint.
- Add atomic project scaffolding, configuration validation, in-process OpenAPI
  export, and machine-readable CLI diagnostics.
- Add bounded SSE fan-out with isolated topics, lifecycle-aware coder/websocket
  handlers, and a grpc-go module with message limits and bounded graceful
  shutdown.

## 0.18.0
- gRPC,SSE,Websockets examples
- Added more CLI commands

## 0.17.1

- Require Go 1.26.6 or newer to avoid known standard-library vulnerabilities.

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
