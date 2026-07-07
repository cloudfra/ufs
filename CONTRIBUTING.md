# Contributing

Thanks for contributing!

## Before you push

Run the full presubmit suite, which mirrors what CI runs on every push and
pull request:

```bash
make presubmit
```

This runs `go fmt`/`go vet` (`lint`) and the full test suite under both
`CGO_ENABLED=0` and the race detector (`check`/`test`).

## Running checks individually

While iterating, it's usually faster to run just the piece you're
changing:

```bash
make lint          # go fmt / go vet
golangci-lint run  # gosec, gocritic, unconvert, unparam, prealloc, bodyclose, noctx, goconst
make test           # unit test suite (CGO_ENABLED=0 and -race)
make test-deflake   # re-run tests under the race detector to catch flakes
```

See the [README](README.md#commands) for the rest.

## Coverage

Codecov enforces coverage on pull requests (see `codecov.yml`): project
coverage can drop by at most 1%, and patched code should be covered at
80% or more.
