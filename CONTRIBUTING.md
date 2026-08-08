# Contributing to Vial

Thank you for helping improve Vial. Keep contributions focused, small, and easy to review.

## Project principles

- Prefer the Go standard library and native platform behavior.
- Solve current, demonstrated needs; avoid scaffolding for possible future features.
- Preserve interoperability with `net/http`.
- Keep the public API explicit and backwards-compatible where practical.
- Fix behavior at the shared root cause and include a regression test.

## Development setup

Vial requires Go 1.23 or newer.

```bash
git clone https://github.com/jrgf/go-vial.git
cd go-vial
make install
make check
```

To exercise the development runner:

```bash
vial dev ./examples/hello
```

`make install` installs `vial` into `GOBIN`, or `$GOPATH/bin` when `GOBIN` is unset. That directory must be on `PATH`.

## Making changes

1. Create a focused branch.
2. Make the smallest complete change that solves the problem.
3. Add or update tests for changed behavior.
4. Run `make check` before opening a pull request.

Do not commit generated files from `bin/`, `.vial/`, coverage profiles, editor settings, or operating-system metadata.

## Tests and quality

`make check` runs:

- The repository-wide 98% coverage gate
- Race detection
- `go vet`
- The CLI build

Tests should verify observable behavior instead of private implementation details. Bug fixes should include a test that fails without the fix. Platform-specific changes must continue to compile on Linux, macOS, and Windows.

Run `go mod tidy -diff` when changing module dependencies.

## Pull requests

Include:

- The problem and why it matters
- A concise description of the solution
- Tests performed
- Compatibility or platform considerations

Keep unrelated refactors out of the same pull request. New dependencies, public APIs, or architectural layers need a concrete use case and clear justification.

## Reporting bugs

Include the Vial version, Go version, operating system, minimal reproduction, expected behavior, and actual behavior.

## Releases

The value printed by `vial version` is the release source of truth. After that
version and the changelog are updated, successful CI on `main` creates the tag,
release title, notes, and binaries unless that version is already published.
Published versions are never overwritten.

## License

By contributing, you agree that your contribution is licensed under the repository's MIT License.
