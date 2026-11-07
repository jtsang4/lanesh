# Contributing to Lanesh

Thanks for your interest in Lanesh. Issues and pull requests are welcome.

## Before you start

- Read the [design document](docs/design.md). It is the source of truth for architecture, protocol and interfaces. Changes that diverge from it should update it in the same pull request.
- For anything larger than a small fix, open an issue first so we can agree on the approach.

## Development setup

- **Go**: the version declared in `go.mod`. With `GOTOOLCHAIN=auto` (the default), Go downloads it for you.
- **Node.js**: 24 or newer.
- **pnpm**: the version pinned in `web/package.json` (`packageManager`). `corepack enable` sets it up.
- **golangci-lint** v2, built with a Go version at least as new as the one in `go.mod`:
  `go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest`
- **GoReleaser** (optional) for `make snapshot`.

Run `make help` to list the available tasks.

## Project layout

The repository follows [golang-standards/project-layout](https://github.com/golang-standards/project-layout):

| Path | Contents |
|---|---|
| `cmd/lanesh` | The single binary entry point |
| `internal/` | Application code (server, daemon, MCP, protocol, storage, ...) |
| `web/` | SvelteKit management UI, embedded into the binary via `web/embed.go` |
| `build/package` | Container image |
| `build/npm` | The `lanesh` npm launcher package |
| `deployments/` | Deployment examples |
| `docs/` | Design and maintainer documentation |

## Making changes

- Keep pull requests focused. Include tests for behaviour changes.
- Run `make lint test` before pushing. CI runs the Go tests on Linux, macOS and Windows.
- Use [Conventional Commits](https://www.conventionalcommits.org/) for commit messages (`feat:`, `fix:`, `docs:`, `refactor:`, `test:`, `ci:`, `chore:`). Release notes are generated from them.
- The web UI targets a high, restrained visual standard. See the design document's UI section before making UI changes.

## Reporting security issues

Please do not open public issues for vulnerabilities. See [SECURITY.md](SECURITY.md).
