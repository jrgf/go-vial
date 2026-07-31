# Roadmap

## v0.1 — HTTP and development-loop MVP

- [x] `net/http`-based application
- [x] Error-returning handlers
- [x] Middleware
- [x] Route groups and path parameters
- [x] JSON binding and responses
- [x] Centralized errors
- [x] Graceful shutdown
- [x] Request ID, logging, recovery
- [x] Automatic source detection
- [x] Debounced candidate builds
- [x] Last-known-good process retention
- [x] Cross-platform process abstraction
- [x] Tests, race tests, vet, and examples

## v0.2 — HTTP ergonomics and tooling

- Route names and reverse URL generation
- Route introspection and `vial routes`
- Query, header, form, and multipart binding
- Validator interface
- JSON test-client helpers
- Config file for development includes/excludes
- Native filesystem event backend with polling fallback
- Better Windows graceful replacement

## v0.3 — Web application capabilities

- `html/template` renderer
- Embedded templates and static files
- Signed cookie sessions
- Flash messages
- CSRF protection
- Browser refresh during development

## Later service-runtime iterations

- Supervised background tasks
- Server-sent events and WebSocket adapters
- Realtime topic abstraction
- First-class gRPC server lifecycle
- Shared HTTP/gRPC middleware concepts
- gRPC streaming adapters
- Distributed brokers and durable job extensions

Major runtime additions begin only after the HTTP kernel and public API have stabilized.
