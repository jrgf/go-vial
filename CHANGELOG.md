# Changelog

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
