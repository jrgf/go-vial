
> [!WARNING]
> This is a learning/pet project  in which I have used AI. If you are uncomfortable with the use of AI this is not the place for you. Of course there will be slop in places I tried to erradicate the most of it,but surely there is.
> My solely goal with this project is to level up in Go and create a light and easy framework to create web apps. If you feel this suits your needs feel free to use it and contribute to the project


# vial

[![CI](https://github.com/jrgf/go-vial/actions/workflows/ci.yml/badge.svg)](https://github.com/jrgf/go-vial/actions/workflows/ci.yml)

`vial` is an early Go web-framework MVP focused on two things:

1. A small, explicit HTTP API built directly on `net/http`.
2. A fast development loop that rebuilds and restarts the application after source changes.

## Current MVP

- Error-returning handlers: `func(*vial.Context) error`
- Standard Go middleware composition
- Named, method-aware routes and path parameters through `http.ServeMux`
- Route groups with scoped middleware
- Named application modules with isolated route registrars
- JSON, text, redirects, and empty responses
- Cached path, query, header, cookie, form, multipart, and JSON binding
- Centralized HTTP errors and transport-neutral application faults
- Request IDs, structured logging, panic recovery, and configurable CORS
- Graceful HTTP shutdown
- Raw `http.Handler` mounting
- Standard `httptest` compatibility
- `vial dev` automatic build-and-restart loop
- Last-known-good process remains online after compilation failures
- No runtime dependencies outside the Go standard library

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

api.Get("/users/{id}", getUser)
api.Post("/users", createUser)
```

Application middleware wraps all requests, including `404` and `405` responses. Group middleware wraps only endpoints registered through that group.

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

## Signed cookie sessions

Vial leaves session policy opt-in. The isolated
[`examples/securecookie`](examples/securecookie) module demonstrates signed
sessions, key rotation, and one-time flash messages with `securecookie`:

```bash
umask 077
openssl rand -hex 32 > /tmp/vial-session-keys
SESSION_KEYS_FILE=/tmp/vial-session-keys VIAL_ALLOW_INSECURE_COOKIE=1 vial dev ./examples/securecookie
```

The example reloads this file every minute and expires sessions after five
minutes. Rotate with `NEW_KEY,OLD_KEY`, newest first; remove the old key after
five minutes.

`VIAL_ALLOW_INSECURE_COOKIE=1` is only for local HTTP. Cookie contents are
authenticated but not encrypted, so never store secrets in them. `SameSite`
is defense in depth, not a replacement for CSRF protection.

## Testing

`testkit` runs the full application lifecycle and cleans up automatically:

```go
server := testkit.Start(t, app)
response := server.JSON(http.MethodPost, "/users", request)
response.RequireStatus(http.StatusCreated)

var created User
response.Decode(&created)
```

Raw `net/http` requests and `httptest` remain supported.

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

## Mount standard handlers

```go
app.HandleHTTP("GET /health", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
    w.WriteHeader(http.StatusNoContent)
}))
```

This is the primary escape hatch for existing middleware, metrics endpoints, profilers, and other `net/http` integrations.

## Development runner

Build the CLI:

```bash
go install ./cmd/vial
```

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
vial dev ./cmd/server -- --config ./config/dev.json
```

Flags:

| Flag | Default | Meaning |
|---|---:|---|
| `--root` | current directory | Directory to watch and use as the build working directory |
| `--debounce` | `250ms` | Quiet period before rebuilding |
| `--restart-timeout` | `3s` | Time allowed for graceful child shutdown |
| `--exclude` | none | Additional ignored name or path; repeatable |
| `--verbose` | false | Print each relevant changed path |

The MVP watcher recursively scans:

- `*.go`
- `go.mod`
- `go.sum`
- `go.work`
- `go.work.sum`

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
```

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
├── middleware/            # request ID, logging, recovery, and CORS
├── internal/dev/          # watcher, builder, runner, and process control
├── cmd/vial/                # development CLI
└── examples/              # runnable applications
```

## Verification

```bash
go test ./...
go test -race ./...
go vet ./...
```

The codebase is also compile-checked for Windows and macOS in CI.

## Known MVP limitations

- Development change detection uses recursive polling rather than native filesystem events.
- Only Go source and module/workspace files trigger rebuilds.
- Windows child replacement uses direct termination; graceful console-event delivery is a later enhancement.
- Route registration becomes immutable after the application is built or first served.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for development setup, quality requirements, and pull request guidance.

## License

MIT
