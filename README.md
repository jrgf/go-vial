# Vial

[![CI](https://github.com/jrgf/go-vial/actions/workflows/ci.yml/badge.svg)](https://github.com/jrgf/go-vial/actions/workflows/ci.yml)

Vial is a Flask-inspired framework for Go's standard `net/http` package. It
adds error-returning handlers, route groups, application lifecycle, background
tasks, test helpers, and a rebuild loop without replacing Go's HTTP types.

Use Vial when an application needs more structure than raw `net/http` but must
still work with standard handlers, middleware, contexts, response writers, and
`httptest`. The core HTTP package uses only the standard library; optional
batteries may use focused dependencies.

## Capabilities

- Error-returning handlers: `func(*vial.Context) error`
- Standard Go middleware composition
- Named, method-aware routes and path parameters through `http.ServeMux`
- Route groups with scoped middleware
- Named application modules with isolated route registrars
- Collision-safe typed context values and trusted-proxy client IPs
- Supervised startup, shutdown, background tasks, liveness, and readiness
- RFC 7240 asynchronous operations with polling and cancellation
- Bounded Server-Sent Event fan-out with heartbeats and slow-consumer policy
- Lifecycle-aware coder/websocket handlers with origin and message limits
- grpc-go lifecycle modules with message limits, h2c, and graceful shutdown
- Encrypted cookie sessions with secure defaults, flash values, and key rotation
- Provider-neutral request identities and authentication/grant guards
- Restrictive browser security headers and bounded local rate limiting
- Route-bounded OpenMetrics, native HTTP tracing integration, and correlated IDs
- `database/sql` transactions, readiness wiring, and embedded forward migrations
- OpenAPI 3.1 generation from routes and Go request and response types
- JSON, text, redirects, and empty responses
- Cached path, query, header, cookie, form, multipart, and JSON binding
- Centralized HTTP errors and transport-neutral application faults
- Request IDs, structured logging, panic recovery, and configurable CORS
- Graceful HTTP shutdown
- Raw `http.Handler` mounting
- Standard `httptest` compatibility
- Project creation, route/config diagnostics, and OpenAPI export through `vial`
- `vial dev` automatic build-and-restart loop
- `vial load` bounded HTTP load checks and CI thresholds
- Last-known-good process remains online after compilation failures
- No runtime dependencies in the core HTTP package

## Examples

1. [`examples/hello`](examples/hello) is the smallest runnable app and
   introduces the development loop.
2. [`examples/json-api`](examples/json-api) demonstrates JSON binding and
   errors. The module, task, upload, SSE, config, secure-cookie, security, CSRF,
   and template examples each cover one concern.
3. [`examples/async`](examples/async) demonstrates
   submission, `Prefer: wait`, polling, cancellation, ownership, idempotency,
   readiness, and metrics with the bounded in-memory executor.
4. [`examples/sse`](examples/sse) uses the bounded `sse.Hub` with a standard
   EventSource response.
5. [`examples/websocket`](examples/websocket) uses `coder/websocket` through a
   standard handler with Vial middleware, limits, and graceful shutdown.
6. [`examples/grpc`](examples/grpc) shares one h2c listener with HTTP routes and
   demonstrates standard interceptors, TLS, streaming, and graceful shutdown.
7. [`examples/observability`](examples/observability) exposes HTTP metrics and
   correlates request and W3C trace IDs in structured logs.
8. [`examples/database`](examples/database) wires PostgreSQL lifecycle,
   readiness, transactions, and embedded migrations.
9. [`examples/openapi`](examples/openapi) generates and serves an OpenAPI 3.1
   document from named routes and Go types.
10. [vial-gateway](https://github.com/jrgf/vial-gateway) and
   [vialboard](https://github.com/jrgf/vialboard) are complete applications.

## Project status

Vial is pre-1.0. The core API audit is complete, while the first-party battery
APIs are still being shaped before the release-candidate freeze. Vial began as
a learning project and accepts AI-assisted contributions. Those changes go
through the same tests, review, security reporting, and compatibility policy
as any other contribution. Read the release notes before using a pre-1.0
version in production.

## Run it

The module lives at `github.com/jrgf/go-vial`.

```bash
git clone https://github.com/jrgf/go-vial.git
cd go-vial

make install
vial version

go test ./...
vial dev ./examples/hello
```

`make install` places `vial` in Go's binary directory (`GOBIN`, or `$GOPATH/bin` by default), which must be on `PATH`.

Open:

```text
http://localhost:8080
http://localhost:8080/users/42
```

Change the response in `examples/hello/main.go` and save. The development runner will build a candidate binary, gracefully stop the previous application only after compilation succeeds, and start the replacement.

A failed build behaves like this:

```text
[vial] source change detected; rebuilding
[vial] building ./examples/hello
examples/hello/main.go:20:2: undefined: broken
[vial] build failed; last successful application remains running
```

## Minimal application

```go
package main

import (
    "context"
    "log"
    "net/http"

    "github.com/jrgf/go-vial"
    "github.com/jrgf/go-vial/middleware"
)

func main() {
    app := vial.New(
        vial.WithDisallowUnknownJSONFields(true),
    )

    app.Use(
        middleware.RequestID(),
        middleware.Logger(),
        middleware.Recover(),
    )

    app.Get("/", func(c *vial.Context) error {
        return c.JSON(http.StatusOK, map[string]string{
            "message": "Hello from vial",
        })
    })

    app.Get("/users/{id}", func(c *vial.Context) error {
        return c.JSON(http.StatusOK, map[string]string{
            "id": c.Param("id"),
        })
    })

    if err := app.Run(context.Background(), ":8080"); err != nil {
        log.Fatal(err)
    }
}
```

`App` implements `http.Handler`, so it also works without the framework server helper:

```go
server := &http.Server{
    Addr:    ":8080",
    Handler: app,
}
```

## Route groups

```go
api := app.Group("/api")
api.Use(authenticationMiddleware)

api.Get("/users/{id}", getUser, vial.RouteName("users.get"))
api.Post("/users", createUser)
```

Application middleware wraps all requests, including `404` and `405` responses. Group middleware wraps only endpoints registered through that group.

Use a route name to build a path for a redirect, link, or `Location` header:

```go
location, err := app.URL("users.get", map[string]string{"id": "A/B"})
// location is /api/users/A%2FB
```

`App.URL` validates and freezes registration on its first call. Call it after
registering all routes, or inside a handler through `context.App().URL`.
Lookups reuse the immutable named-route index and support concurrent callers.
Pass raw parameter values. Missing or extra parameters, empty or slash-only
single segments, and dot segments return errors. `{path...}` preserves slashes,
permits an empty tail or a
trailing slash, and rejects leading or repeated slashes. Paths include group
prefixes and omit hosts, schemes, queries, and fragments. Add query strings with
`net/url.Values`.

## Modules

A route group organizes HTTP endpoints. A module organizes one named business
capability, such as users, billing, inventory, or administration.

```go
type UserModule struct {
    service *UserService
}

func NewModule(service *UserService) *UserModule { return &UserModule{service: service} }

func (module *UserModule) Name() string { return "users" }

func (module *UserModule) Register(registrar *vial.Registrar) error {
    users := registrar.Group("/users")
    users.Get("/", module.list, vial.RouteName("users.list"))
    users.Post("/", module.create, vial.RouteName("users.create"))
    users.Get("/{id}", module.get, vial.RouteName("users.get"))
    return nil
}
```

Register modules before building or serving the application:

```go
if err := app.Register(
    users.NewModule(userService),
    orders.NewModule(orderService),
); err != nil {
    return err
}
```

Registration is atomic. Each module gets an isolated registrar for routes,
middleware, lifecycle hooks, supervised tasks, and health endpoints. See the
runnable [`examples/modules`](examples/modules).

Modules contain application functionality. Extensions provide technical
infrastructure, such as sessions, databases, telemetry, authentication, task
supervision, or a gRPC server. Vial does not require an extension interface;
ordinary constructors and application options remain sufficient.

## Realtime

The [`sse`](sse) package writes events and provides bounded process-local
fan-out. [`vialws`](vialws) closes coder/websocket connections with the Vial
request lifecycle. [`vialgrpc`](vialgrpc) mounts grpc-go with message limits and
bounded graceful shutdown. Deployment limits and examples are in
[`docs/realtime.md`](docs/realtime.md).

## JSON binding

```go
type CreateUserRequest struct {
    Name  string `json:"name"`
    Email string `json:"email"`
}

app.Post("/users", func(c *vial.Context) error {
    var request CreateUserRequest
    if err := c.BindJSON(&request); err != nil {
        return err
    }

    return c.JSON(http.StatusCreated, request)
})
```

The default body limit is 16 MiB. Applications can override it and reject
unknown JSON fields:

```go
app := vial.New(
	vial.WithMaxBodySize(10 << 20),
    vial.WithDisallowUnknownJSONFields(true),
)
```

`Context.Bind` combines fields tagged with `path`, `query`, `header`, `cookie`,
and `form` with JSON or form bodies. Uploaded files use `Context.FormFile`; see
the runnable [`examples/upload`](examples/upload).

## HTML templates

Keep parsing in `html/template`; `render` buffers the result before committing
the response:

```go
parsed, err := template.ParseFS(webFS, "templates/*.html")
if err != nil {
    return err
}
views := render.New(parsed)

app.Get("/", func(c *vial.Context) error {
    return views.HTML(c, http.StatusOK, "home", page)
})
```

The runnable [`examples/web`](examples/web) adds a validated, CSRF-protected
form and uses `embed.FS` with the native `http.FileServerFS` for static assets:

```bash
VIAL_ALLOW_INSECURE_COOKIE=1 vial dev ./examples/web
```

The insecure-cookie flag is only for local HTTP.

## Encrypted cookie sessions

The [`session`](session) package provides authenticated and encrypted
client-side sessions, one-time flash values, secure cookie defaults, and live
key rotation. The runnable [`examples/securecookie`](examples/securecookie)
module demonstrates the complete flow:

```bash
umask 077
openssl rand -hex 32 > /tmp/vial-session-keys
SESSION_KEYS_FILE=/tmp/vial-session-keys VIAL_ALLOW_INSECURE_COOKIE=1 vial dev ./examples/securecookie
```

The example reloads this file every minute and expires sessions after five
minutes. Rotate with `NEW_KEY,OLD_KEY`, newest first; remove the old key after
the longest configured session lifetime.

`VIAL_ALLOW_INSECURE_COOKIE=1` is only for local HTTP. Sessions are encrypted
but remain client-side and limited to one cookie. `SameSite` is defense in
depth, not a replacement for CSRF protection. See
[`docs/sessions.md`](docs/sessions.md) for the API and deployment rules.

## Authentication and authorization

The [`auth`](auth) package resolves an optional identity once per request and
provides route guards for authentication and application-defined grants. It
does not own users, passwords, tokens, or an authorization database. The
[`examples/securecookie`](examples/securecookie) application demonstrates
session-backed identity resolution with protected `/me` and `/admin` routes.

See [`docs/authentication.md`](docs/authentication.md) for middleware ordering,
401/403 behavior, and integration guidance.

## Security middleware

`middleware.SecurityHeaders()` adds a restrictive Content Security Policy,
referrer and framing controls, MIME sniffing protection, and HSTS on direct TLS
requests. `middleware.RateLimit(...)` provides bounded in-process token buckets
keyed by `Context.ClientIP()` by default. The runnable
[`examples/security`](examples/security) application demonstrates both.

Use a gateway or shared store when a limit must span replicas. See
[`docs/security.md`](docs/security.md) for proxy, HSTS, key-capacity, and CSP
guidance.

## Observability

`middleware.HTTPMetrics` records request counts, active requests, and a fixed
duration histogram using registered route patterns instead of raw paths.
`App.UseHTTP` accepts standard `net/http` middleware, including tracing
libraries. `middleware.TraceContext` adds a validated trace ID to request state
and structured logs alongside `X-Request-ID`.

The runnable [`examples/observability`](examples/observability) application
exposes `/metrics`. See [`docs/observability.md`](docs/observability.md) for
tracer integration, metric names, middleware order, and deployment guidance.

## Database

The [`sqlkit`](sqlkit) package adds transaction and embedded migration helpers
to `database/sql`. Existing application hooks own the pool lifecycle:

```go
app.OnStart(db.PingContext, migrator.Migrate)
app.OnStop(func(context.Context) error { return db.Close() })
app.Readiness("/ready", db.PingContext)
```

`sqlkit.InTx` commits on success and rolls back on errors or panics. `Migrator`
applies sorted `.sql` files once, stores SHA-256 checksums, and rejects edited
history. See [`docs/database.md`](docs/database.md) and the runnable
[`examples/database`](examples/database) PostgreSQL application.

## OpenAPI 3.1

The [`openapi`](openapi) package generates deterministic OpenAPI 3.1 JSON from
`App.Routes`, `RouteName`, and Go request and response types. It understands
Vial's `path`, `query`, `header`, `cookie`, `form`, and `json` binding tags.

```go
err := openapi.Mount(app, "/openapi.json", openapi.Config{
    Title:   "Notes API",
    Version: "1.0.0",
    Operations: map[string]openapi.Operation{
        "notes.create": {
            Request: createNoteRequest{},
            Responses: map[int]openapi.Response{
                http.StatusCreated: {Body: note{}},
            },
        },
    },
})
```

`openapi:"required"` marks documented fields as required; application
validation remains authoritative. `Operation.RequestSchema` and `Response.Schema`
accept explicit JSON Schema for custom JSON types, constraints, and examples.
Embedded fields and `json:",string"` follow Go's JSON encoding rules.
See [`docs/openapi.md`](docs/openapi.md) and
the runnable [`examples/openapi`](examples/openapi) application.

## Testing

Public API suites live in [`tests/`](tests), grouped by package. Private unit
tests remain beside their source files. `go test ./...` includes both, and
`make coverage` instruments production packages across the test tree.

`testkit` runs the full application lifecycle and cleans up automatically:

```go
server := testkit.Start(t, app)
response := server.JSON(http.MethodPost, "/users", request)
response.RequireStatus(http.StatusCreated)

var created User
response.Decode(&created)
```

Raw `net/http` requests and `httptest` remain supported.

## Async operations

Register a bounded executor once; Vial starts it before serving HTTP and drains
accepted work during graceful shutdown:

```go
executor := async.NewMemoryExecutor(
    async.WithWorkers(8),
    async.WithQueueSize(256),
    async.WithTaskTimeout(5*time.Minute),
)
executor.Handle("reports.generate", func(ctx context.Context, job async.Job) (any, error) {
    var request GenerateReportRequest
    if err := job.Decode(&request); err != nil {
        return nil, err
    }
    return reportService.Generate(ctx, request)
})
app.Async(executor)

app.Post("/reports", func(c *vial.Context) error {
    preference, err := vial.ParsePrefer(c.Header("Prefer"))
    if err != nil {
        return err
    }
    if !preference.RespondAsync {
        return vial.BadRequest("respond_async_required", "Use Prefer: respond-async")
    }
    operation, err := c.Async().Submit(c.Request().Context(), vial.SubmitRequest{
        Name:             "reports.generate",
        Payload:          GenerateReportRequest{ReportID: "report_123"},
        IdempotencyKey:   c.Header("Idempotency-Key"),
        IdempotencyScope: currentUserID(c),
        Metadata:         map[string]string{"user_id": currentUserID(c)},
    })
    if err != nil {
        return err
    }

    operation, completed, err := c.Await(operation, 3*time.Second)
    if err != nil {
        return err
    }
    if completed {
        if operation.Error != nil {
            return operation.Error
        }
        if operation.Status == vial.OperationCancelled {
            return vial.Conflict("operation_cancelled", "The operation was cancelled")
        }
        return c.JSON(http.StatusCreated, operation.Result)
    }
    return c.Accepted(operation)
})

authorize := func(c *vial.Context, operation *vial.Operation) error {
    if operation.Metadata["user_id"] != currentUserID(c) {
        return vial.NotFound("operation_not_found", "Operation not found")
    }
    return nil
}
app.Get("/operations/{id}", vial.OperationStatusHandler(executor, authorize))
app.Delete("/operations/{id}", vial.OperationCancelHandler(executor, authorize))
app.Get("/metrics/async", vial.AsyncMetricsHandler(executor))
app.Readiness("/ready", executor.Ready)
```

`Accepted` returns `202 Accepted` with `Location`, `Retry-After`, and a status
URL. A full queue returns `503 Service Unavailable`; the same operation name,
authenticated-owner scope, and idempotency key returns the existing operation.
`Prefer: respond-async, wait=3` waits up to the smaller client preference and
server maximum before falling back to `202`. Return resource URLs instead of
embedding large results.

> **The memory executor is not durable.** Every pending, running, completed,
> and idempotency record is lost when the process exits. Use it only when losing
> work is acceptable.

For durable work, use the PostgreSQL adapter with any `database/sql` PostgreSQL
driver already selected by the application:

```go
executor := asyncpostgres.New(db,
    asyncpostgres.WithWorkers(16),
    asyncpostgres.WithLeaseDuration(30*time.Second),
)
executor.Handle("reports.generate", generateReport)
app.Async(executor)

operation, err := executor.Submit(ctx, vial.SubmitRequest{
    Name:             "reports.generate",
    Payload:          request,
    IdempotencyKey:   idempotencyKey,
    IdempotencyScope: authenticatedUserID,
    Retry: vial.RetryPolicy{
        MaxAttempts:    5,
        InitialBackoff: time.Second,
        MaxBackoff:     time.Minute,
    },
})
```

The adapter creates its schema idempotently by default. It persists operations
and results, leases rows with `FOR UPDATE SKIP LOCKED`, renews visibility while
handlers run, recovers expired leases after crashes, supports multiple replicas,
and leaves exhausted deliveries as inspectable `failed` operations. Disable
automatic schema creation with `asyncpostgres.WithAutoMigrate(false)` after
applying [`contrib/asyncpostgres/schema.sql`](contrib/asyncpostgres/schema.sql)
through the application's migration system.


## Background tasks

Keep shared queues application-owned; use contexts only for cancellation:

```go
type Event struct {
    Name string
}

events := make(chan Event, 128)

app.Post("/events", func(c *vial.Context) error {
    select {
    case events <- Event{Name: "user.created"}:
        return c.NoContent(http.StatusAccepted)
    case <-c.Request().Context().Done():
        return c.Request().Context().Err()
    default:
        return vial.NewHTTPError(503, "queue_full", "Event queue is full")
    }
})

app.Go("event-consumer", func(ctx context.Context) error {
    for {
        select {
        case <-ctx.Done():
            return ctx.Err()
        case event := <-events:
            if err := handleEvent(ctx, event); err != nil {
                return err
            }
        }
    }
})
```

This queue is in-memory and loses pending events when the process stops. Use a
database outbox or external broker when events must be durable. See the
runnable [`examples/events`](examples/events).

## Configuration

Use `config.Load` with an initialized struct. Existing values are defaults,
JSON files are applied in order, and environment variables win:

```go
type AppConfig struct {
    Environment string `json:"environment" env:"VIAL_ENV"`
    HTTP        config.HTTP `json:"http"`
}

func (configuration *AppConfig) Validate() error {
    if configuration.Environment == "" {
        return errors.New("environment is required")
    }
    return nil
}

configuration := AppConfig{Environment: "development"}

if err := config.Load(
    &configuration,
    config.OptionalFile("config.json"),
); err != nil {
    log.Fatal(err)
}

if err := app.Run(context.Background(), configuration.HTTP.Address()); err != nil {
    log.Fatal(err)
}
```

An optional `config.json` can override the defaults:

```json
{
  "http": {
    "port": 9000
  }
}
```

Environment variables take final precedence:

```bash
VIAL_HTTP_PORT=9090 go run .
```

Use `config.File` when a file is required. Configuration types may implement
`config.Validator`; validation runs after all sources are loaded.

`config.HTTP.Address()` defaults to `127.0.0.1:8080`. Production deployments
that must accept external traffic can set `VIAL_HTTP_HOST=0.0.0.0` explicitly.
See the runnable [`examples/config`](examples/config) application.

## Errors

Handlers return an `error`. Business failures use transport-neutral faults:

```go
return fault.New(
    fault.NotFound,
    "user_not_found",
    "The requested user was not found",
)
```

Use `HTTPError` only when an HTTP-specific status or response header is needed.

Default response:

```json
{
  "error": {
    "code": "user_not_found",
    "message": "The requested user was not found"
  }
}
```

Unexpected errors are rendered as a generic `500` response. Their internal details remain in server logs.
Unmatched routes and unsupported methods use the same error handler and return
`not_found` and `method_not_allowed` codes; `405` responses include `Allow`.

Enable [RFC 9457 Problem Details](https://www.rfc-editor.org/rfc/rfc9457.html)
before building the application:

```go
app.SetErrorHandler(vial.ProblemDetailsErrorHandler)
```

Errors then use `application/problem+json` with `type: "about:blank"`, the HTTP
status and title, a public `detail`, and the existing `code` and optional
validation `fields` extensions. The renderer preserves `Allow`, `Retry-After`,
and authentication challenges. Internal causes, fault metadata, and request URLs
stay out of the response. Explicit `HTTPError.Message` values remain public.
Applications using the default renderer retain the existing JSON envelope.
See [`examples/openapi`](examples/openapi) for named `Location` headers, Problem
Details, and matching response schemas.

## Mount standard handlers

```go
app.HandleHTTP("GET /health", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
    w.WriteHeader(http.StatusNoContent)
}))
```

Mount a raw handler for existing middleware, metrics endpoints, profilers, and
other `net/http` integrations.

## Command-line workflow

Build the CLI:

```bash
go install ./cmd/vial
```

Create a minimal service:

```bash
vial new --module example.com/service ./service
cd ./service
go mod tidy
go run .
```

The generated service includes liveness, readiness, request IDs, logging,
panic recovery, security headers, and graceful shutdown.

### Development runner

Usage:

```bash
vial dev [flags] [package] [-- application arguments]
```

Examples:

```bash
vial dev ./examples/hello
vial dev --verbose ./cmd/server
vial dev --debounce 500ms ./cmd/server
vial dev --exclude generated ./cmd/server
vial dev --watch '*.html' --watch '*.css' ./examples/web
vial dev ./cmd/server -- --config ./config/dev.json
```

Flags:

| Flag | Default | Meaning |
|---|---:|---|
| `--root` | current directory | Directory to watch and use as the build working directory |
| `--debounce` | `250ms` | Quiet period before rebuilding |
| `--restart-timeout` | `3s` | Time allowed for graceful child shutdown |
| `--exclude` | none | Additional ignored name or path; repeatable |
| `--watch` | none | Additional filename or root-relative path pattern; repeatable |
| `--verbose` | false | Print each relevant changed path |

The watcher recursively scans these files by default:

- `*.go`
- `go.mod`
- `go.sum`
- `go.work`
- `go.work.sum`

Use `--watch '*.html'`, `--watch '*.css'`, or `--watch '*.sql'` to rebuild after
embedded assets change. Patterns without `/` match filenames at any depth;
patterns with `/`, such as `static/*.css`, match paths relative to `--root`.
Patterns use Go's `path.Match` syntax: `*` does not cross `/`, and `**` has no
special meaning. Use forward slashes on every platform and quote patterns to
prevent shell expansion. Added, modified, and deleted files trigger rebuilds;
exclusions still take precedence.

It ignores:

- `.git`
- `.vial`
- `vendor`
- `node_modules`
- `tmp`
- `dist`

Generated binaries are written under `.vial/bin` and are ignored by the watcher.

Inspect routes without starting the server:

```bash
vial routes ./examples/hello
vial routes --json ./examples/hello
```

This command supports applications that call `App.Run`; applications using a
custom `http.Server` can call `App.Routes` directly.

Validate configuration and application build setup without starting the server:

```bash
vial doctor ./examples/config
vial doctor --json ./examples/config
vial config --json ./examples/config
```

`config` reports validation only. It never prints application-owned values or
secrets.

Export the OpenAPI document served by an application without binding a port:

```bash
vial openapi ./examples/openapi
vial openapi --output openapi.json ./examples/openapi
```

The default endpoint is `/openapi.json`; override it with `--path`. Export
builds the application but does not run startup or shutdown hooks.

Run a bounded load check against a deployed endpoint:

```bash
vial load --workers 2000 --duration 30s --timeout 5s http://localhost:8080/
vial load --max-error-rate 1 --max-p95 250ms http://localhost:8080/
```

### CLI contract

- `vial`, `help`, `--help`, `-h`, and subcommand help exit with status 0.
- Unknown commands, invalid arguments, runtime failures, and failed load
  thresholds exit with status 1.
- `vial new --json` writes `directory`, `module`, `go`, and `vial` fields.
- `vial routes --json` writes an indented JSON array of `vial.Route` values to
  standard output.
- `vial doctor --json` writes `ok`, `routes`, `named_routes`, and `go` fields.
- `vial config --json` writes `{"valid": true}` after application construction
  and route validation succeed.
- `vial openapi` writes an OpenAPI 3.1 JSON document to standard output or the
  file selected by `--output`.
- `vial version --verbose` writes stable `version=`, `commit=`, and `go=` lines
  to standard output.
- `vial version --json` writes `version`, `commit`, and `go` fields.
- `vial load` writes its final summary to standard output and progress to
  standard error, keeping redirected summaries clean.

Human-readable help, tables, progress text, and error wording may improve within
1.x. Exit behavior and the machine-readable formats above are compatibility
contracts.

### Replacement sequence

```text
source change
    ↓
debounce
    ↓
build unique candidate binary
    ├── failure → retain current process
    └── success
            ↓
       interrupt current process
            ↓
       wait up to restart timeout
            ↓
       start candidate
```

Builds never run concurrently. Changes detected during a build remain queued for the next pass.

## Repository layout

```text
.
├── app.go                 # application, registration, and net/http adapter
├── context.go             # request and response helpers
├── binding.go             # query, form, multipart, and JSON body limits
├── errors.go              # HTTP error model and renderer
├── server.go              # server lifecycle and graceful shutdown
├── auth/                  # request identities and authorization guards
├── session/               # encrypted client-side cookie sessions
├── sqlkit/                # database/sql transactions and migrations
├── openapi/               # OpenAPI 3.1 generation and document handler
├── sse/                   # bounded Server-Sent Event fan-out
├── vialws/                # coder/websocket lifecycle adapter
├── vialgrpc/              # grpc-go lifecycle module
├── middleware/            # request ID, logging, recovery, browser policy, and rate limits
├── internal/dev/          # watcher, builder, runner, and process control
├── cmd/vial/              # development and load-check CLI
└── examples/              # runnable applications
```

## Verification

```bash
go test ./...
go test -race ./...
go vet ./...
```

Use the existing protocol client at the integration boundary. Extra Vial
wrappers would hide behavior that applications need to verify.

| Battery | Supported test path |
|---|---|
| HTTP, sessions, authentication, security, observability | `testkit.Start`, `Server.JSON`, `Server.Multipart`, and `RequireRoute` |
| SSE | `sse.Hub`, `testkit.Start`, `http.Client`, and `bufio.Reader` |
| WebSocket | `vialws.NewHandler`, `testkit.Start`, and `coder/websocket` |
| gRPC | `vialgrpc.New`, `testkit.Start`, and the generated grpc-go client |
| SQL | `database/sql` with a test driver; run database integration tests for the selected production driver |
| Async operations | In-memory executor tests plus the selected persistent adapter's integration tests |
| OpenAPI | Generate or fetch JSON, decode it, and assert the documented operation and schema |

The maintained examples under `examples/` are runnable reference tests for
each row.

The codebase is also compile-checked for Windows and macOS in CI.

## Known limitations

- Development change detection uses recursive polling rather than native filesystem events.
- Non-Go assets require explicit `--watch` patterns; `go:embed` directives are not discovered automatically.
- Windows child replacement uses direct termination; graceful console-event delivery is a later enhancement.
- Route registration becomes immutable after the application is built or first served.

## Go support

Vial requires Go 1.26.6 or newer and currently tests Go 1.26 and Go 1.27.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for development setup, quality requirements, and pull request guidance.
Report vulnerabilities privately through [SECURITY.md](SECURITY.md).

## License

MIT
