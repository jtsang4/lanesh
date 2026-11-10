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
| `GET` | `/lanes/{lane}/keys` | Join and access keys: `kind`, `name`, `prefix`, `created_at`, `expires_at`, `last_used_at`, `revoked_at`, active keys first and then newest first. Tokens are never returned again. Query: `kind` (`join` or `access`), `status` (`active` or `inactive`, meaning revoked or expired), `limit` (max 200; without it every key is returned, `0` returns only the count) and `offset`. `X-Total-Count` gives the number of matching keys. |
| `POST` | `/lanes/{lane}/keys` | Body `{"kind": "join" \| "access", "name", "expires_in_days"?}`. Returns `201` with `{"key", "token"}`; the token is shown only here. |
| `DELETE` | `/lanes/{lane}/keys/{key}` | Revokes the key. |

## Projects

A project names where some work lives (`locations`: a machine and a path each) and what an agent should know about it (`context`, free Markdown up to 20 KB). `{project}` is a project ID or name.

| Method | Path | Description |
|---|---|---|
| `GET` | `/lanes/{lane}/projects` | Projects by name: `id`, `name`, `summary` (the first paragraph of the context), `locations` with `machine_name` and `online`, `revision`, `updated_at`, `updated_by`. |
| `POST` | `/lanes/{lane}/projects` | Body `{"name", "context"?, "locations"?: [{"machine_id", "path"}]}`. Returns `201` with the project. `409` if the lane has a project of that name (ignoring case). |
| `GET` | `/lanes/{lane}/projects/{project}` | The project, with each location checked on its machine now: `info` (`exists`, `git`, `branch`, `worktree`, `agent_files`) or a `note` saying why it was not checked. |
| `PATCH` | `/lanes/{lane}/projects/{project}` | Body with any of `name`, `context`, `locations` (the whole list), and `base_revision`. With `base_revision`, returns `409` and the current `project` if it changed since. |
| `DELETE` | `/lanes/{lane}/projects/{project}` | Deletes the project and its history. Files on machines are not touched. |
| `GET` | `/lanes/{lane}/projects/{project}/revisions` | The last 20 revisions, newest first, each a full snapshot (`revision`, `name`, `context`, `locations`, `updated_at`, `updated_by`, `updated_by_name`). Restore one by sending its fields with `PATCH`. |

## Audit log

| Method | Path | Description |
|---|---|---|
| `GET` | `/lanes/{lane}/audit` | Newest first. Query: `machine`, `action`, `actor`, `before` (an entry ID: pass the last ID of a page to get the next one), `limit` (default 100, max 500). Entries include `actor_name` and `machine_name` when the actor or machine is a key or machine of the lane, including revoked keys and removed machines. |

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
| `project.create`, `project.update`, `project.delete` | Project changes by the admin or an agent (`name`, `revision`, `changes`) |

Command output and file contents are never stored.

## Daemon and MCP endpoints

These are not part of the admin API but are listed for completeness:

| Method | Path | Authentication | Description |
|---|---|---|---|
| `POST` | `/lanes/{lane}/join` | join key | Registers a machine and returns its machine credential. |
| `GET` | `/lanes/{lane}/connect` | machine credential | Daemon WebSocket (control protocol, see the design document §6). |
| `DELETE` | `/lanes/{lane}/membership` | machine credential | A machine leaves the lane. |
| `GET`, `POST`, `DELETE` | `/lanes/{lane}/mcp` | access key (`Authorization: Bearer` or `?key=`) | MCP Streamable HTTP endpoint. Optional `X-Lanesh-Machine` header or `?machine=` names the agent's own machine. |
| `POST`, `GET` | `/transfers/{id}/upload`, `/transfers/{id}/download` | one-time ticket | Transfer data streams. Uploads may be split into `?part=N` requests, each preceded by an empty `?part=N&wait=1`, the last one with `final=1`; `202` asks for a request again (see design §7). |
| `GET` | `/install.sh`, `/install.ps1` | none | Install scripts for the server's version. |
| `GET` | `/healthz` | none | `{"status": "ok", "version": "…"}` |
