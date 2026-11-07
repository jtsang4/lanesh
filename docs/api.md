# Admin API

The management API is used by the web UI and the `lanesh lane` / `key` / `machine` commands. All endpoints are under `/api/v1`, accept and return JSON, and report errors as `{"error": "message"}`.

## Authentication

Either:

- `Authorization: Bearer <admin token>`, for scripts and the CLI, or
- the `lanesh_session` cookie set by `POST /api/v1/login`, used by the web UI.

Cross-origin, cookie-authenticated, state-changing requests are rejected.

| Method | Path | Description |
|---|---|---|
| `POST` | `/login` | Body `{"token": "<admin token>"}`. Sets an HttpOnly, `SameSite=Strict` session cookie (30 days). Rate limited per client. |
| `POST` | `/logout` | Ends the session. |
| `GET` | `/session` | `{"authenticated": bool, "version": "…"}`. Does not require authentication. |

## Instance

| Method | Path | Description |
|---|---|---|
| `GET` | `/info` | `version`, `commit`, `protocol`, `public_url`, `public_url_configured`, `data_dir`, `started_at`, `lanes`, `machines`, `online` |

## Lanes

| Method | Path | Description |
|---|---|---|
| `GET` | `/lanes` | List lanes with `machine_count` and `online_count`. |
| `POST` | `/lanes` | Body `{"name"}`. Returns `201` with the lane. |
| `GET` | `/lanes/{lane}` | One lane. |
| `PATCH` | `/lanes/{lane}` | Body `{"name"}`. |
| `DELETE` | `/lanes/{lane}` | Deletes the lane with its keys and memberships and disconnects its machines. |

## Machines

| Method | Path | Description |
|---|---|---|
| `GET` | `/lanes/{lane}/machines` | Machines with `online`, `connected_at`, `last_seen`, `joined_at`, `policy`, `skill_count`, `skills_hash` and the live `environment` (`os`, `arch`, `hostname`, `user`, `home`, `default_shell`, `shells`, `version`). |
| `PATCH` | `/lanes/{lane}/machines/{machine}` | Body `{"name"}`; `409` if another machine in the lane has that name. |
| `DELETE` | `/lanes/{lane}/machines/{machine}` | Removes the machine from the lane and disconnects it. The machine must join again with a join key. |
| `GET` | `/lanes/{lane}/machines/{machine}/skills` | The skill index (`skills`, `shadowed`, `hash`), whether it is `live` or the last stored snapshot, and `updated_at`. |
| `GET` | `/lanes/{lane}/machines/{machine}/skills/{skill}?path=` | Without `path`: `SKILL.md`, the skill directory and its file tree (`files`). With `path`: that file (`content`, `encoding` `utf-8` or `base64`, `size`, `truncated`). `503` while the machine is offline. |
| `GET` | `/lanes/{lane}/skills` | Skills aggregated across the lane's machines, with `consistent: false` when copies differ. |

## Keys

| Method | Path | Description |
|---|---|---|
| `GET` | `/lanes/{lane}/keys` | Join and access keys: `kind`, `name`, `prefix`, `created_at`, `expires_at`, `last_used_at`, `revoked_at`. Tokens are never returned again. |
| `POST` | `/lanes/{lane}/keys` | Body `{"kind": "join" \| "access", "name", "expires_in_days"?}`. Returns `201` with `{"key", "token"}`; the token is shown only here. |
| `DELETE` | `/lanes/{lane}/keys/{key}` | Revokes the key. |

## Audit log

| Method | Path | Description |
|---|---|---|
| `GET` | `/lanes/{lane}/audit` | Newest first. Query: `machine`, `action`, `actor`, `before` (an entry ID, for pagination), `limit` (default 100, max 500). |

Each entry has `id`, `time`, `lane_id`, `actor` (an access key ID, a machine ID or `admin`), `action`, `machine_id`, `detail` and `result`. Actions:

| Action | Recorded when |
|---|---|
| `exec` | An agent starts a command (`command`, `cwd`, `skill`, `job_id`, `status`, `exit_code`) |
| `job.exited` | A command finishes (`exit_code`, `duration_ms`) |
| `job.write`, `job.kill` | An agent writes to or stops a job |
| `file.read`, `file.write` | Inline file operations (`path`, `bytes`) |
| `transfer` | A transfer between machines (`from_path`, `to_machine`, `to_path`, `bytes`, `files`) |
| `machine.join`, `machine.leave`, `machine.remove` | Membership changes |
| `key.create`, `key.revoke`, `lane.create`, `lane.delete` | Administrative changes |

Command output and file contents are never stored.

## Daemon and MCP endpoints

These are not part of the admin API but are listed for completeness:

| Method | Path | Authentication | Description |
|---|---|---|---|
| `POST` | `/lanes/{lane}/join` | join key | Registers a machine and returns its machine credential. |
| `GET` | `/lanes/{lane}/connect` | machine credential | Daemon WebSocket (control protocol, see the design document §6). |
| `DELETE` | `/lanes/{lane}/membership` | machine credential | A machine leaves the lane. |
| `GET`, `POST`, `DELETE` | `/lanes/{lane}/mcp` | access key (`Authorization: Bearer` or `?key=`) | MCP Streamable HTTP endpoint. Optional `X-Lanesh-Machine` header or `?machine=` names the agent's own machine. |
| `POST`, `GET` | `/transfers/{id}/upload`, `/transfers/{id}/download` | one-time ticket | Transfer data streams. |
| `GET` | `/install.sh`, `/install.ps1` | none | Install scripts for the server's version. |
| `GET` | `/healthz` | none | `{"status": "ok", "version": "…"}` |
