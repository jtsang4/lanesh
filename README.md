# Lanesh

**Share skills and shells across your machines through one MCP endpoint.**

[![CI](https://github.com/jtsang4/lanesh/actions/workflows/ci.yml/badge.svg)](https://github.com/jtsang4/lanesh/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

> [!WARNING]
> Lanesh is in early development. The design is settled, but most features are not implemented yet. See [the design document](docs/design.md) and the [roadmap](#status).

## Why

An AI agent can only use the machine it runs on. Your skills are spread across a laptop, a dev box, and a GPU server, each with its own tools, credentials and network access. Copying skills around drifts out of sync, and SSH gives the agent a shell but tells it nothing about what each machine can do.

Lanesh gives an agent one MCP endpoint that reaches all of your machines:

- **Discover** which machines are online and what global skills each one has installed
- **Read** a skill's `SKILL.md` and its scripts and references, on demand
- **Run** commands on a specific machine, including long-running background jobs
- **Move** files between machines

## How it works

```
Agent ──HTTP (MCP)──▶ Lanesh server ◀──WebSocket── machine A (lanesh daemon)
                      (self-hosted)  ◀──WebSocket── machine B (lanesh daemon)
```

- You self-host a **server** and create one or more **lanes**. A lane is a group of machines that trust each other.
- Each machine runs the **lanesh daemon** as a background service and joins a lane. The daemon dials out to the server, so machines behind NAT work too.
- Any agent adds the lane's MCP URL and an access key to its MCP config. It never needs to install anything.

The daemon reads global skills from `~/.agents/skills` and `~/.claude/skills` (`~/.agents/skills` wins on name conflicts).

## Planned MCP tools

| Tool | Purpose |
|---|---|
| `list_machines` | Machines in the lane, their status and skill counts |
| `describe_machine` | A machine's environment, policy and installed skills; required before other tools touch that machine |
| `read_skill` | `SKILL.md`, the skill's file tree, or any file inside the skill |
| `exec` | Run a command; long commands continue as background jobs |
| `job_read` / `job_write` / `job_kill` / `job_list` | Manage background jobs |
| `read_file` / `write_file` | Small inline file operations |
| `transfer` | Stream files or directories between machines |

## Status

- [x] Design ([docs/design.md](docs/design.md), in Chinese)
- [x] Project skeleton, CI and release pipeline
- [ ] Server core: lanes, credentials, daemon connections
- [ ] MCP: machines, skills, describe gating
- [ ] Exec, jobs and file transfer on macOS, Linux and Windows
- [ ] Background service management and self-update
- [ ] Web management UI
- [ ] First release

## Installation

There is no release yet. Once released, Lanesh will be available as:

- a container image: `ghcr.io/jtsang4/lanesh`
- an npm package: `npx lanesh`
- standalone binaries on [GitHub Releases](https://github.com/jtsang4/lanesh/releases)

## Development

Requirements: Go (the version in `go.mod` is fetched automatically), Node.js 24+ and pnpm (the version pinned in `web/package.json`).

```sh
make web          # build the UI (embedded into the binary)
make build        # build ./bin/lanesh
make test         # Go, web and npm launcher tests
make lint
make dev-server   # run the server on :8080
make dev-web      # run the UI dev server with API proxy
```

See [CONTRIBUTING.md](CONTRIBUTING.md) for details.

## License

[MIT](LICENSE)
