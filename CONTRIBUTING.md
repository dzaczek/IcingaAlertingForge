# Contributing

## Development Setup

```bash
git clone https://github.com/dzaczek/IcingaAlertingForge.git
cd IcingaAlertingForge
cp .env.example .env  # edit with your Icinga2 credentials
```

### Prerequisites

- Go 1.24+
- Docker (optional, for testenv)
- pre-commit (recommended)

### Quick Start

```bash
make build      # compile the binary
make test       # run unit tests
make lint       # run golangci-lint
make ci         # full CI pipeline (lint + test + coverage + build)
```

## Commit Conventions

This project follows [Conventional Commits](https://www.conventionalcommits.org/):

- `feat:` — new feature
- `fix:` — bug fix
- `chore:` — maintenance, deps, tooling
- `test:` — tests only
- `ci:` — CI/CD changes
- `docs:` — documentation
- `refactor:` — restructuring without behavior change
- `perf:` — performance improvement
- `build:` — build system or release changes

## Testing

```bash
make test             # all unit tests
make test-unit        # unit tests only (no race detector)
make coverage         # test with coverage HTML report
make smoke            # smoke test with docker compose
```

### Adding Tests

- Use table-driven tests
- Use stdlib `testing` package; `testify` is available but not required
- Place fixtures in `testdata/` directories
- Target ≥70% coverage for new packages

## Linting

```bash
make lint             # golangci-lint run
make vulncheck        # govulncheck
```

Configuration is in `.golangci.yml`. Do not weaken lint rules — fix the code.

## Documentation

- API docs: `docs/`
- Wiki: `wiki/` (synced to GitHub Wiki)
- Architecture: `docs/guides/architecture-and-setup.md`

## Pull Requests

1. Create a feature branch from `main`
2. Make focused changes with Conventional Commit messages
3. Run `make ci` locally
4. Open a PR using the template
5. Ensure all CI checks pass

## Questions?

Open a [discussion](https://github.com/dzaczek/IcingaAlertingForge/discussions) or issue.
