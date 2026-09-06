# Vial 1.0.0-rc.1 review

Reviewed September 5, 2026, against the working tree based on `d295a80`.

The implementation passes local RC validation. Hold publication until the
candidate changes are committed, the required CI matrix passes on that commit,
and the public guides referenced by the README are included in Git.

V1 now includes OpenAPI body-schema overrides, embedded-asset watch patterns,
named-route URLs, and opt-in Problem Details. Public API tests are consolidated
into 28 files under 12 folders in `tests/`, within the existing module.
Package-local tests and standalone example modules retain their existing homes.

| Check | Result |
| --- | --- |
| `make check`, Go 1.26.6 | Passed; 94.7% coverage, race checks, vet, examples, and CLI build |
| Go 1.27.0 race suite | Passed, including the consolidated test tree |
| Lint | Zero issues |
| Fuzzing | All ten targets passed 100,000 iterations each |
| Vulnerability scans | No vulnerabilities found in the root module or the securecookie, WebSocket, gRPC, and database examples |
| Release binaries | Linux, macOS, and Windows on amd64 and arm64 built twice with matching bytes |
| Example and benchmark commands | OpenAPI export and the relocated benchmark entry points passed |
| Release classification | RC and stable versions, including build metadata, passed the shell check |

The review fixed inconsistent inference for malformed JSON tags across Go
versions, an unrepresentable slash-only URL parameter, HTTP error-header casing,
stale database-example dependency metadata, and missing database CI checks.
Regression tests cover the fixes. RC releases now explicitly use GitHub's
prerelease flag and are excluded from Latest. See the
[CLI release options](https://cli.github.com/manual/gh_release_create).

Before publication:

1. Commit the complete candidate and require green CI for its exact commit.
   Local cross-compilation does not replace runtime tests on Linux and Windows.
2. Include the seven README-linked guides in the repository: realtime, sessions,
   authentication, security, observability, database, and OpenAPI. The current
   `.gitignore` excludes all of `docs/`; none of these guides is tracked.

After publishing the RC, verify a clean installation, downloadable checksums,
SBOM, and provenance attestation. Local binaries are marked `d295a80-dirty` and
are validation artifacts, not published release evidence.

Live PostgreSQL was not exercised; its example was compiled and dependency-scanned.
Downstream Vialboard checks and the planned 30-day production burn-in remain
outstanding. Start that burn-in with the RC and complete it before stable 1.0.

Local logs, the reproducible validation script, and binary pairs are under
`.vial/rc-review/`. The scanner was built with Go 1.27.0 from the CI-pinned
`govulncheck` v1.7.0.
