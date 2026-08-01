# Changelog

## Unreleased

- Read-only route catalog through `App.Routes`
- Route inspection through `vial routes` and `vial routes --json`
- Framework-owned `404` and `405` errors with native `Allow` headers
- Exact Vial root and trailing-slash routes instead of `ServeMux` catch-alls
- Primitive query and URL-encoded form binding
- Size-limited multipart form/file handling with temporary-file cleanup

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
