# Tests

Public API tests live here, grouped by the package they exercise. They remain in
the main Go module, so `go test ./...` includes them without workspace setup or
module replacements. `tests/vial` contains the framework integration tests,
external-module compatibility test, client-IP fuzz target, and HTTP benchmarks.

Tests that access unexported symbols stay beside their source files, as Go
requires. This includes private unit tests, internal packages, and application
examples. Standalone example modules and the Fiber comparison keep their own
tests because they have separate dependencies.

From the repository root:

```sh
go test ./tests/...
make check
go test -run '^$' -bench '^BenchmarkDispatch$' -benchmem ./tests/vial
go test -run '^$' -fuzz '^FuzzClientIPForwarding$' -fuzztime 100000x ./tests/vial
```

`make coverage` uses `-coverpkg=./...` so moved tests still measure the production
packages they exercise. CI uses the same coverage target and updated fuzz and
benchmark paths.
