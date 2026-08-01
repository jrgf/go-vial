
> [!WARNING]
> This is a learning/pet project  in which I have used AI. If you are uncomfortable with the use of AI this is not the place for you. Of course there will be slop in places I tried to erradicate the most of it,but surely there is.
> My solely goal with this project is to level up in Go and create a light and easy framework to create web apps. If you feel this suits your needs feel free to use it and contribute to the project


# vial

`vial` is an early Go web-framework MVP focused on two things:

1. A small, explicit HTTP API built directly on `net/http`.
2. A fast development loop that rebuilds and restarts the application after source changes.

The repository is intentionally narrow. gRPC, realtime transports, durable jobs, templates, sessions, OpenAPI, and typed endpoint generation are future iterations—not unfinished parts of this release.

## Current MVP

- Error-returning handlers: `func(*vial.Context) error`
- Standard Go middleware composition
- Method-aware routes and path parameters through `http.ServeMux`
- Route groups with scoped middleware
- JSON, text, redirects, and empty responses
- Strict JSON binding with body limits
- Centralized structured error responses
- Request IDs, structured logging, and panic recovery
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

The application can enforce a body limit and reject unknown fields:

```go
app := vial.New(
    vial.WithMaxBodySize(2 << 20),
    vial.WithDisallowUnknownJSONFields(true),
)
```

## Errors

Handlers return an `error`. Known application failures use `HTTPError` helpers:

```go
return vial.NotFound(
    "user_not_found",
    "The requested user was not found",
)
```

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
├── context.go             # request helpers and JSON binding
├── errors.go              # HTTP error model and renderer
├── server.go              # server lifecycle and graceful shutdown
├── middleware/            # request ID, logging, and recovery
├── internal/dev/          # watcher, builder, runner, and process control
├── cmd/vial/                # development CLI
└── examples/              # runnable applications
```

See [`docs/architecture.md`](docs/architecture.md) for the internal request and development-runner flows.

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
- Framework errors are JSON, while unmatched `ServeMux` `404`/`405` responses still use standard-library formatting.
- Route registration becomes immutable after the application is built or first served.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for development setup, quality requirements, and pull request guidance.

## License

MIT
