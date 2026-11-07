# Agent Guidelines for Lanesh

Lanesh lets an AI agent discover and use global skills and shells across multiple machines through one self-hosted MCP endpoint. It consists of a server, a per-machine daemon, and a management web UI, all shipped as one Go binary.

## Source of truth

- `docs/design.md` defines the architecture, protocol, MCP tools, deployment and release process. Read the relevant section before changing behaviour.
- If an implementation needs to diverge from the design, update `docs/design.md` in the same change. Do not let code and design drift apart.
- `docs/releasing.md` describes the release process.

## Layout

- `cmd/lanesh`: the only entry point; subcommands select server, daemon or CLI behaviour.
- `internal/`: all application code. Package boundaries follow the design document (§14).
- `web/`: SvelteKit SPA. `web/embed.go` embeds `web/build/app`; `web/build/.gitkeep` must stay so Go compiles without a UI build.
- `build/package/Dockerfile`: image built by GoReleaser from prebuilt binaries.
- `build/npm`: the `lanesh` npm launcher. It contains no binary; it downloads release assets named `lanesh_<version>_<os>_<arch>[.exe]`, which must stay in sync with `.goreleaser.yaml`.

## Commands

```sh
make web     # build the UI
make build   # build ./bin/lanesh
make test    # go test, svelte-check, npm launcher tests
make lint    # golangci-lint and web lint
```

## Conventions

### Go

- Prefer the standard library. Add a dependency only when it clearly pays for itself, and use its latest release.
- Lanesh must work on macOS, Linux and Windows. Use `path/filepath` for OS paths, put OS-specific code in `_unix.go` / `_windows.go` files with build constraints, and never assume a POSIX shell on Windows.
- Use `log/slog`, pass `context.Context` through blocking calls, and wrap errors with `%w`.
- Write table-driven tests next to the code. CI runs tests on all three platforms.
- Code must pass `golangci-lint` with the repository config.

### Web UI

- Svelte 5 runes and SvelteKit with `adapter-static`; the UI is a client-rendered SPA served by the Go server.
- Use the project skills `svelte-code-writer` and `svelte-core-bestpractices` when writing Svelte code.
- Tooling comes from the Vite/VoidZero ecosystem: `oxlint` for linting and `oxfmt` for formatting (`pnpm run lint`, `pnpm run format`); `svelte-check` covers Svelte compiler diagnostics and types.
- Visual direction: minimal and flat, refined through typography, spacing and detail. Avoid oversized buttons and poster-style hero sections.
- Quality bar: work at the level of Awwwards, Webby or FWA winners. After each iteration, screenshot the UI in a browser, review it against the checklist in `docs/design.md` §10.4, and keep improving until it meets that bar.
- English is the default UI language; Simplified Chinese is provided through i18n.

### Dependencies

- Keep dependencies on their latest versions within compatibility limits. TypeScript stays on 6.x until SvelteKit and svelte-check support 7.x.
- Dependabot opens weekly update PRs for Go modules, web packages, GitHub Actions and the Docker base image.

### Skills

- Project skills live in `.agents/skills/`; `.claude/skills/` contains relative symlinks to them.
- Third-party skills are managed with `npx skills` and recorded in `skills-lock.json`.

### General

- Do not hardcode paths, hostnames or credentials from a developer's machine.
- Use Conventional Commits (`feat:`, `fix:`, `docs:`, `refactor:`, `test:`, `ci:`, `chore:`).
