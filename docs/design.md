# Lanesh 设计文档

> 状态：v1 设计稿
> 范围：本文档定义 Lanesh v1 的目标、架构、协议、接口、部署与发布方式，是后续实现的依据。

## 1. 概述

### 1.1 Lanesh 是什么

Lanesh 把分散在多台机器上的 Agent skill 和执行环境，通过一个自托管的中心服务和一个 HTTP MCP 端点，聚合成 Agent 可以发现、按需加载和调度的统一资源池。

- 中心服务上可以创建多个 **Lane**。每个 Lane 是一组互相信任的机器。
- 每台机器运行 Lanesh 守护进程并加入一个或多个 Lane，把本机的全局 skill 和执行能力提供给 Lane。
- 任意 Agent 只要在 MCP 配置里填上 Lane 的 MCP URL 和凭证，就能：
  - 查看 Lane 中有哪些机器
  - 了解某台机器的环境和已安装的全局 skill
  - 按需读取 skill 的 `SKILL.md` 及其附属文件
  - 在指定机器上执行命令、读写文件、在机器之间传输文件

### 1.2 要解决的问题

- Agent 的能力被限制在它所在的那台机器上。skill 分散在不同机器，各机器上的工具、凭证、网络权限和硬件也不同，在 A 机器上运行的 Agent 看不到也用不了 B 机器的能力。
- 复制 skill 会带来同步和版本不一致问题；逐台配置 SSH 成本高，而且 Agent 无法感知远端有什么能力。
- 需要在不一次性塞满上下文的前提下，保留 skill 的渐进加载方式：先看描述，再读 `SKILL.md`，再按需读附属文件。

### 1.3 设计原则

- **轻量可接入**：只消费的机器什么都不用装；提供能力的机器一条命令即可加入，并以后台服务方式常驻。
- **提供方自主**：一台机器开放什么能力，由该机器本地的配置决定，消费方无法越权。
- **凭证即角色**：角色由所持凭证决定，不由连接参数自行声明。
- **控制面与数据面分离**：命令和元数据走 WebSocket 控制通道，文件内容走独立的 HTTP 数据流。
- **自托管优先**：单二进制或容器镜像部署，只依赖一个 SQLite 文件。
- **不侵入本地 skill 目录**：Lanesh 只读取 `~/.agents/skills` 和 `~/.claude/skills`，从不向任何本地 skill 目录写入内容，包括为远程 skill 生成本地占位或镜像。远程 skill 只通过 MCP 工具呈现给 Agent。

### 1.4 v1 范围

包含：

- 中心服务：Lane、凭证、机器注册、MCP 端点、审计日志、Web 管理界面
- 守护进程：skill 发现、命令执行（含后台任务）、文件读写、跨机器文件传输
- 后台服务安装与管理，自动升级
- 平台：macOS、Linux、Windows（amd64 / arm64）
- 分发：GitHub Releases 二进制、npm 包 `lanesh`、GHCR 容器镜像

v1 不包含（见 §16 路线图）：

- PTY / 交互式终端（接口预留）
- 远程 MCP connector 所需的 OAuth
- 多用户与租户体系（每个人部署自己的实例）
- Web 终端
- 端到端加密、点对点直连传输

## 2. 术语

| 术语 | 含义 |
|---|---|
| **Server** | 自托管的中心服务。负责鉴权、路由、MCP 端点、Web 管理界面和持久化 |
| **Lane** | 一组互相信任的机器的逻辑分组，也是 MCP 的作用域。一个 Server 上可以有多个 Lane |
| **Machine** | 运行 Lanesh 守护进程的机器。一台机器可以同时加入多个 Lane |
| **Daemon** | 机器上的 Lanesh 守护进程（`lanesh daemon`），以当前用户身份作为后台服务运行 |
| **Consumer** | 通过 MCP 调用 Lane 能力的 Agent |
| **Admin token** | 实例管理员凭证，用于管理 Lane 和发放凭证 |
| **Join key** | Lane 级凭证，用于让新机器加入 Lane |
| **Machine credential** | 机器加入成功后由 Server 签发的专属凭证，守护进程之后用它连接 |
| **Access key** | Lane 级凭证，Agent 用它调用该 Lane 的 MCP 端点 |
| **Briefing** | `describe_machine` 返回的简报令牌，其他操作该机器的工具都需要它 |

## 3. 架构

```
                       ┌────────────────────────── Server ──────────────────────────┐
 Agent ──HTTP (MCP)──▶ │  /lanes/{lane}/mcp      MCP 端点（Streamable HTTP）          │
                       │  /lanes/{lane}/connect  守护进程 WebSocket 接入              │ ◀──WS── Machine A (daemon)
 Browser ─────────────▶│  /api/v1/*  /           管理 API 与 Web UI                   │ ◀──WS── Machine B (daemon)
                       │  /transfers/{id}/*      文件传输数据流                        │ ◀──HTTP─ (传输时)
                       │  SQLite (/data)         Lane / 凭证 / 机器 / 审计              │
                       └─────────────────────────────────────────────────────────────┘
```

- **控制通道**：守护进程主动向 Server 建立 WebSocket 长连接（每个 Lane 成员关系一条），因此位于 NAT 或内网中的机器也能加入。Server 通过这条连接向机器下发请求并接收结果。
- **数据通道**：文件传输时，源机器和目标机器分别对 Server 发起独立的 HTTP 请求，Server 在两者之间流式转发，不落盘。这样大文件传输不会阻塞控制通道上的命令。
- **MCP 端点**：由 Server 直接以 Streamable HTTP 提供。消费方不需要安装任何东西。
- **状态**：连接、MCP 会话、任务路由等运行态数据都在 Server 内存中；Lane、凭证、机器、审计日志、skill 索引快照持久化在 SQLite。
- **单实例**：由于连接状态在内存里，Server 只支持单实例运行。对个人自托管场景这不是问题。

### 3.1 角色组合

角色由“跑什么”和“持有什么凭证”决定，不需要额外的角色参数：

| 机器的用途 | 需要做的事 |
|---|---|
| 只消费 | 在 Agent 的 MCP 配置中填 MCP URL 和 access key |
| 只提供 | 运行守护进程加入 Lane |
| 两者兼有 | 两者都做 |

注意：跨机器传输的目标必须是 Lane 中的机器。只消费、未运行守护进程的机器无法作为 `transfer` 的目标。

### 3.2 长连接的资源开销

在 Go 中，一条空闲的 WebSocket 连接大约占用几十 KB 内存，外加心跳流量。个人规模（几十台机器）总开销可以忽略。相比之下，轮询方案要么延迟高，要么空转请求更多，所以选择长连接。

Server 真正的开销是文件传输时转发的带宽，通过控制面与数据面分离、流式转发、大小限制来控制。

## 4. 身份、凭证与权限

### 4.1 凭证

| 凭证 | 前缀 | 作用域 | 来源 | 用途 |
|---|---|---|---|---|
| Admin token | `lsa_` | 实例 | 部署时通过环境变量 `LANESH_ADMIN_TOKEN` 设置；未设置时首次启动自动生成并打印到日志（只打印一次） | 管理 API、Web UI 登录 |
| Join key | `lsj_` | Lane | Admin 发放，可有多个，可设过期时间 | 新机器加入 Lane |
| Machine credential | `lsm_` | 机器 × Lane | 加入成功时由 Server 签发 | 守护进程连接 Lane |
| Access key | `lsk_` | Lane | Admin 发放，可有多个（建议每个 Agent 或每台机器一个） | 调用 MCP |

- 所有凭证均为高熵随机串。Server 只存哈希，并用常量时间比较。明文只在创建时显示一次。
- 带前缀便于识别，也便于被密钥扫描工具发现。
- 凭证优先通过 `Authorization: Bearer <token>` 传递。仅当某些客户端不支持自定义 header 时，才允许使用 URL 参数 `?key=`；Server 和审计日志都不得记录完整 URL 中的凭证。
- 任意凭证都可以单独吊销。吊销 machine credential 会立即断开该机器在该 Lane 的连接；吊销 join key 不影响已加入的机器。
- 删除 Lane 时，断开该 Lane 下的所有连接并作废其全部凭证。

### 4.2 加入流程

```
lanesh join https://lanesh.example.com/lanes/<lane-id> --key lsj_xxx [--name my-mac] [--no-exec]
```

1. 守护进程用 join key 调用 `POST /lanes/{lane}/join`，提交机器 ID、名称、系统信息；如果本机已经持有该 Lane 的 machine credential，一并提交作为证明。
2. Server 校验 join key，在该 Lane 中登记机器，并签发新的 machine credential。如果这台机器在该 Lane 中已有生效的成员关系，必须提交当前凭证才能重新加入（凭证随之轮换），否则返回 `409`。这样仅凭 join key 无法冒用已有机器的 ID 接管它；被管理员移除的机器可以用 join key 重新加入。
3. 守护进程把 machine credential 保存到本地凭证文件（权限 0600），之后用它建立 WebSocket 连接。
4. 如果还没安装后台服务，则安装并启动（见 §11）。

### 4.3 机器身份

- 首次运行时生成一个全局唯一的机器 ID，保存在 `~/.lanesh/machine-id`。重连、重装守护进程都沿用同一个 ID。
- 显示名默认取主机名，可以修改。同一 Lane 内名称冲突时，Server 自动追加后缀。名称不能以 `mch_` 开头，避免和机器 ID 混淆。
- MCP 工具里的 `machine` 参数同时接受机器名和机器 ID，ID 优先匹配。
- 机器上报的系统信息和 skill 快照按 `(Lane, 机器)` 分别保存，一个 Lane 无法影响另一个 Lane 看到的内容。
- 一台机器可以加入多个 Lane，这些 Lane 可以在不同的 Server 上。每个成员关系各有一个 machine credential 和一条 WebSocket 连接。

### 4.4 提供方策略

机器本地的配置决定它向 Lane 开放什么，消费方无法越权：

```toml
# ~/.lanesh/config.toml
[policy]
expose_skills = true   # 是否开放 skill 列表与读取
allow_exec = true      # 是否开放执行与通用文件操作（exec、read_file、write_file、transfer）
```

- `allow_exec` 默认为 `true`。`lanesh join` 时会明确提示风险，`--no-exec` 可在加入时关闭。
- `allow_exec` 同时管控通用文件读写和传输，因为能任意写文件就等同于能执行命令。
- 策略也可以按 Lane 覆盖（`[lanes.<lane-id>.policy]`）。
- `describe_machine` 会返回当前生效的策略，让 Agent 知道这台机器能做什么。

## 5. 守护进程

### 5.1 本地目录

```
~/.lanesh/                 # Windows: %USERPROFILE%\.lanesh；可用 LANESH_HOME 覆盖
  bin/lanesh[.exe]         # 受管二进制，后台服务从这里运行
  config.toml              # 机器名、策略、更新设置、已加入的 Lane
  credentials.toml         # machine credential（0600）
  machine-id
  logs/daemon.log
  logs/audit.log           # 本机执行与文件操作的审计记录
```

### 5.2 skill 发现

- 扫描目录（按优先级从高到低）：
  1. `~/.agents/skills`
  2. `~/.claude/skills`
- 每个子目录中包含 `SKILL.md` 的视为一个 skill。从 `SKILL.md` 的 YAML frontmatter 读取 `name` 和 `description`；没有 `name` 时用目录名。
- **去重**：同名 skill 只保留优先级最高的一个（`~/.agents/skills` 优先），被覆盖的记入 `shadowed` 列表。
- 跟随符号链接（常见做法是 `~/.claude/skills/*` 链接到 `~/.agents/skills/*`）。真实路径相同的条目自然合并。
- 为每个 skill 计算内容哈希（基于目录内所有文件的相对路径与内容），用于判断不同机器上的同名 skill 是否一致。
- 用文件系统监听维护索引。索引变化时，通过控制通道推送 `skills.changed` 事件，Server 更新内存索引和 SQLite 快照。监听失效时退化为定期重新扫描。

### 5.3 命令执行

**执行环境**

- 以守护进程所属用户身份执行，不提权。
- **Unix 环境变量**：系统服务启动的进程 PATH 很精简，通过 nvm、brew、pipx 等安装的命令都找不到。守护进程启动时会以登录 + 交互方式运行用户默认 shell（`$SHELL -l -i -c` 输出 `env -0`，带超时），把得到的环境缓存下来作为执行环境；`lanesh doctor --refresh-env` 可以手动刷新。执行时用 `$SHELL -c '<command>'` 并注入该环境。
- **Windows shell**：默认 shell 优先 `pwsh`，没有则用 `powershell.exe`；也可以通过参数显式指定 `cmd`。
- **Windows 编码**：中文等非 UTF-8 系统的控制台默认使用本地代码页（例如 936/GBK）。执行 PowerShell 时设置 UTF-8 输出编码，执行 cmd 时先 `chcp 65001`；如果输出仍然不是合法 UTF-8，再按系统 ANSI 代码页解码。返回给 Agent 的统一是 UTF-8。
- **进程树管理**：Unix 使用独立进程组，终止时向整个进程组发信号；Windows 使用带 `KILL_ON_JOB_CLOSE` 的 Job Object。确保终止任务时不会留下孤儿进程。

**任务模型**

- 每次 `exec` 都创建一个任务（job），分配不可猜测的 `job_id`。
- `exec` 在 `timeout_ms`（等待时长，默认 30 秒，可调）内结束时，直接返回退出码和输出；超时仍未结束时，返回 `status: running` 和 `job_id`，任务转入后台继续运行。
- `background: true` 时立即返回 `job_id`。
- 输出写入每个任务独立的环形缓冲（stdout 和 stderr 分开，默认上限各 16MB），超出部分丢弃最早的内容并标记。任务结束后保留一段时间（默认 1 小时）供读取。
- 返回给 Agent 的输出有内联上限（默认约 30k 字符，保留头部和尾部），完整内容可以通过 `job_read` 分段读取。
- 任务记录发起它的 access key（`owner`）。`job_list` 默认只列出当前 access key 发起的任务。
- 任务按 Lane 隔离：一个 Lane 发起的任务，只能在同一个 Lane 里读取、写入和终止。
- 前台执行（未设置 `background`）在写完可选的 `stdin` 后立即关闭标准输入，避免命令等待输入而挂起；`background: true` 时保持标准输入打开，供 `job_write` 使用。
- 守护进程重启时，所有正在运行的任务都会被终止。

**PTY 预留**

- 执行层抽象为 `Runner` 接口，v1 只有管道实现（stdin / stdout / stderr）。
- `exec` 的 `pty` 参数在 schema 中保留；v1 传 `true` 时返回 `unsupported` 错误。
- 后续实现 PTY（Unix 用 pty，Windows 用 ConPTY）时不需要修改协议的整体结构。

### 5.4 文件操作

- `read_file` / `write_file` 通过控制通道内联传输内容，适合小文件和文本，内联上限默认 1MB。超过时返回错误，提示改用 `transfer`。
- 文本按 UTF-8 返回；二进制或非 UTF-8 内容使用 `base64` 编码。
- `write_file` 支持 `overwrite`、`create_dirs`、`mode`（Unix 权限位），写入时先写临时文件再原子重命名。
- 路径支持 `~` 展开，相对路径以用户 home 为基准。

## 6. 控制协议

### 6.1 连接

- 端点：`GET /lanes/{lane}/connect`，`Authorization: Bearer <machine credential>`，升级为 WebSocket。
- 消息使用 JSON 文本帧，信封格式：

```json
{ "id": "req-123", "type": "exec.start", "payload": { } }
```

- 消息类型分为四类：
  - **请求 / 响应**：`type` 为方法名，响应的 `type` 为 `result` 或 `error`，并携带相同的 `id`
  - **事件**：由守护进程主动推送，例如 `skills.changed`、`job.exited`
  - **取消**：`cancel`，携带要取消的请求 `id`
  - **心跳**：使用 WebSocket ping/pong 帧

### 6.2 握手

连接建立后，守护进程首先发送 `hello`：

```json
{
  "type": "hello",
  "payload": {
    "protocol": 1,
    "version": "1.2.3",
    "machine_id": "…",
    "name": "my-mac",
    "os": "darwin", "arch": "arm64",
    "policy": { "expose_skills": true, "allow_exec": true }
  }
}
```

Server 回复 `welcome`，包含 Server 版本、协议版本范围和 Lane 信息；不兼容时回复 `error`，并在必要时提示升级。

### 6.3 版本与兼容

- 产品版本遵循语义化版本；协议版本是一个整数。Server 声明自己接受的协议版本范围 `[min, max]`。
- 在同一个协议版本内，所有变更都必须是加法式的（只新增字段和方法），这样 Server 能够接受比自己新的守护进程。
- 守护进程收到 `welcome` 后执行自动升级判断（见 §11.4）。

### 6.4 心跳与重连

- 心跳间隔 25 秒，必须小于常见反向代理的空闲超时（Traefik、Cloudflare 等约在 60–100 秒）。连续 3 次未收到 pong 视为断线。
- 断线后按指数退避重连（1 秒起，最大 60 秒，带随机抖动）。
- Server 侧记录机器的在线状态和最后在线时间。机器离线后在列表中显示为离线，不会消失。

### 6.5 方法

| 方法 | 方向 | 说明 |
|---|---|---|
| `machine.describe` | Server → Daemon | 返回环境信息、策略和 skill 索引 |
| `skill.read` | Server → Daemon | 读取 skill 目录内的文件或文件树 |
| `exec.start` | Server → Daemon | 启动任务 |
| `job.read` / `job.write` / `job.kill` / `job.list` | Server → Daemon | 任务操作 |
| `file.read` / `file.write` | Server → Daemon | 内联文件操作 |
| `transfer.send` / `transfer.receive` | Server → Daemon | 指示守护进程向数据通道上传或从中下载 |
| `skills.changed` | Daemon → Server | 推送 skill 索引变更 |
| `job.exited` | Daemon → Server | 任务结束通知（用于审计） |

## 7. 文件传输

```
transfer(from=A, from_path, to=B, to_path)

Server ──transfer.send(ticket)──▶ A ──POST /transfers/{id}/upload──▶ Server ──┐ io.Pipe
Server ──transfer.receive(ticket)─▶ B ◀─GET /transfers/{id}/download── Server ◀┘
```

1. Server 校验两台机器的策略，创建传输记录，生成一次性 ticket。
2. 通过控制通道分别通知源机器和目标机器。
3. 源机器上传，目标机器下载，两者都用 ticket 鉴权。Server 用管道直接转发，不落盘。
4. 任一方在超时时间内没有连上，传输失败并通知另一方。
5. 目录传输时打成 tar 流（Go `archive/tar`），保留相对路径和 Unix 权限位；在 Windows 上对权限位做近似处理。
6. 目标机器先写入临时路径，完成并校验后再原子移动到目标路径。`overwrite` 默认为 `false`。
7. tar 流以一个 PAX 全局头作为结束标记，普通文件无法伪造它。接收方只有读到结束标记才会替换目标路径；上传中途失败时 Server 会中断下载流。这样被截断的传输不会覆盖目标路径原有的内容。
8. 返回文件总字节数、文件数和传输流的 sha256。
9. 源和目标可以是同一台机器，同样经由 Server 中转。

单次传输大小上限可通过 `LANESH_TRANSFER_MAX_BYTES` 配置。

## 8. MCP 接口

### 8.1 端点与鉴权

- 端点：`https://<server>/lanes/{lane}/mcp`，使用 Streamable HTTP 传输。
- 鉴权：`Authorization: Bearer <access key>`，或者在不支持 header 的客户端中使用 `?key=`。
- 可选标识本机：`X-Lanesh-Machine: <machine>` 或 `?machine=<machine>`。如果 Agent 所在的机器本身也在 Lane 中，`list_machines` 会把它标记为 `self: true`，帮助 Agent 区分本地和远程。

Claude Code 配置示例：

```bash
claude mcp add --scope user --transport http lanesh https://lanesh.example.com/lanes/<lane-id>/mcp \
  --header "Authorization: Bearer lsk_xxx"
```

Web UI 会为常见客户端生成可以直接复制的配置。

### 8.2 Server instructions

`instructions` 按 Lane 动态生成（`internal/mcp/briefing.go` 中的 `Instructions`），每次建立 MCP 连接时根据 Lane 的当前状态生成：

- **前 512 字符**是使用流程：先 `describe_machine` 拿简报令牌、每次调用带上令牌、调用返回简报时阅读后带新令牌重试、在 skill 所在的机器上执行脚本、用 `transfer` 在机器之间传文件。OpenAI 建议把关键信息放在 instructions 的前 512 字符。
- **其后**是补充说明，以及连接时的机器清单：名称、系统/架构、默认 shell、skill 数量、是否禁止执行、是否离线。
- **总长度不超过 2,000 字符**。Claude Code 只保留每个服务 instructions 的前 2,048 字符；机器太多时，清单末尾改为"还有 N 台，请调用 `list_machines`"。
- 不包含 skill 列表和简报令牌。Agent 必须真正看过某台机器的简报，才能操作它。
- 不能假设客户端会把 instructions 放进系统提示：规范只说"可以"，实测 Claude Code 2.1.285 把它作为第一条用户消息里的附件注入。所以 instructions 只用于引导，正确性由 §8.4 的令牌机制保证。

服务端为每个 Lane 缓存一个 MCP server 实例，instructions 内容变化时才重建。

### 8.3 工具

所有接受 `machine` 参数的工具，机器离线时都返回明确的错误。

**`list_machines()`**

返回 Lane 中所有机器：`id`、`name`、`os`、`arch`、`online`、`last_seen`、`version`、`allow_exec`、`skill_count`、`self`。

**`describe_machine(machine)`**

返回机器简报：

- 环境：`os`、`arch`、`hostname`、`user`、`home`、`default_shell`、可用 shell 列表、Lanesh 版本
- 生效的策略
- skill 列表：`name`、`description`、`source`（`agents` 或 `claude`）、`path`（绝对路径）、`hash`
- `shadowed`：因同名被覆盖的 skill（名称和来源）

同时返回简报令牌 `briefing`（形如 `build-box:k3f9q2ab`），其他操作这台机器的工具都需要它（见 §8.4）。

**`read_skill(machine, briefing, skill, path?, offset?, limit?)`**

- 不传 `path` 时，返回 `SKILL.md` 内容、skill 目录的绝对路径，以及目录文件树（含文件大小）。
- 传 `path`（相对 skill 目录）时，读取对应文件；只允许访问 skill 目录内的路径，拒绝路径穿越。
- 大文件支持 `offset` / `limit` 分段读取；二进制文件以 base64 返回。
- 只依赖 `expose_skills`，不依赖 `allow_exec`。

**`exec(machine, briefing, command, cwd?, skill?, shell?, env?, stdin?, timeout_ms?, background?, pty?)`**

- `skill`：把工作目录设为该 skill 的目录；与 `cwd` 同时出现时，`cwd` 视为相对 skill 目录的路径。
- 返回：`job_id`、`status`（`exited` / `running`）、`exit_code`、`stdout`、`stderr`、截断标记、耗时。
- `pty` 在 v1 中保留，传 `true` 时返回 `unsupported`。

**`job_read(machine, job_id, stream?, offset?, limit?)`**、**`job_write(machine, job_id, data?, close_stdin?)`**、**`job_kill(machine, job_id, signal?)`**、**`job_list(machine, all?)`**

- 分别用于分段读取输出、写入 stdin、终止任务、列出任务。任务保存在机器上，所以都需要 `machine`。
- `job_list` 默认只列出当前 access key 发起的任务，`all: true` 列出该 Lane 在这台机器上的全部任务。

**`read_file(machine, briefing, path, offset?, limit?, encoding?)`**、**`write_file(machine, briefing, path, content, encoding?, overwrite?, create_dirs?, mode?)`**

- 内联文件读写，超过内联上限时提示改用 `transfer`。

**`transfer(from_machine, from_briefing, from_path, to_machine, to_briefing, to_path, overwrite?)`**

- 在两台机器之间传输文件或目录，见 §7。

### 8.4 简报令牌（describe gating）

目的：确保 Agent 在操作一台机器之前，已经了解它的环境和已安装的 skill。

**为什么用令牌**

- MCP 2026-07-28 版协议取消了协议层会话和 `Mcp-Session-Id`（SEP-2567、SEP-2575），服务端无法知道两次请求是否来自同一个对话。规范给出的做法是："需要跨调用状态的服务端，使用由服务端签发、作为普通工具参数传递的句柄"。简报令牌就是这样的句柄。
- 不能依赖 instructions 承载简报（见 §8.2 的长度和位置限制）。
- 官方 Skills 扩展（SEP-2640）暂不适用，见 §16。

**令牌**

- 形如 `build-box:k3f9q2ab`。冒号后是对 (Lane, 机器 ID, 机器 skill 索引哈希) 的 HMAC，取 40 位，用 base32 编码为 8 个字符。密钥保存在 Server 的 SQLite 中，Server 不需要保存任何会话状态。
- **短而稳定**：机器的 skill 不变时，令牌不变，多次 describe 得到同一个令牌，不会出现多个令牌互相混淆；skill 一变，令牌随之改变。
- 机器名部分只是方便模型区分，校验时不使用，所以机器改名不会使令牌失效。
- 令牌不是安全凭据（访问控制由 access key 负责），只用于确认 Agent 看过简报，所以 40 位足够。

**校验**

- `read_skill`、`exec`、`read_file`、`write_file` 需要对应机器的 `briefing`；`transfer` 需要来源和目标机器各自的令牌。`job_*` 操作已经存在的任务，不需要令牌。
- Server 先用自己缓存的机器状态校验；不匹配时向机器获取一次最新简报再校验，以免因为 Server 的缓存落后而误判。
- 令牌缺失、错误或过时（skill 已变化）时：**不执行任何操作**，直接返回最新简报和当前令牌，形式如下。开头一行写明下一步动作：用哪个工具、哪个参数（`transfer` 是 `from_briefing` / `to_briefing`）、哪个令牌重试，并说明不需要再调用 `describe_machine`。Agent 阅读后重试一次即可：

  ```
  Nothing was run on "build-box": its briefing token is unknown, or the machine's skills changed since it was issued.
  Next step: read the briefing below, then repeat the same exec call with briefing "build-box:k3f9q2ab". This reply already is the machine's full briefing, so do not call describe_machine for it.

  Machine: build-box (linux/amd64), user dev, home /home/dev
  Shell: zsh (available: bash, zsh, sh)
  Allows: shared skills yes; commands and file access yes
  Skills (3); read one with read_skill, run its scripts here with exec and skill set:
  - deploy-preview: …
  ```

- `transfer` 两端都需要简报时，在一次返回中给出两台机器的简报。

**相对于各会话自身认知的变化报告**

不同会话对同一台机器的认知不同：旧会话可能已经读过某个 skill 的旧版本，需要知道具体哪个变了；新会话拿到的就是最新状态，不需要任何"更新"提示。所以变化是相对于**调用方所持令牌**来计算的，而不是按时间：

- Server 为每个 (Lane, 机器) 保存最近 20 个 skill 索引版本（`skill_history`）。每次签发令牌都要先经过一次 describe，并把结果写入历史，所以令牌对应的版本一定在历史里，除非它已经被更新的版本挤出去了。这里记录的是机器的状态历史，与会话无关。
- 收到过时令牌时，Server 在历史版本中逐个校验，找到令牌签发时对应的版本，与当前版本按 skill 名称比较，在简报开头列出：
  ```
  Changes since your last briefing:
  + log-rotate (new)
  ~ deploy-preview (updated; if you read it before, read it again with read_skill before relying on it)
  - pdf-tools (removed)
  ```
  描述变化或任何文件内容变化都算作 updated，因为每个 skill 都有自己的内容哈希。如果只是同名 skill 的优先级发生变化，会单独说明。
- **没有令牌**（例如新会话）时，返回不带变化标记的普通简报；令牌无法识别，或者早于保留的历史时，只说明令牌未知，不列差异。

**效果**

- Agent 忘记令牌（例如上下文被压缩）时，下一次调用会直接收到简报，恢复只需一次重试，并且恰好在它需要时重新了解了这台机器。
- 子 Agent 没有从主 Agent 拿到令牌时，第一次操作同样会收到简报。
- 与协议版本无关，新旧客户端行为一致。

## 9. Server

### 9.1 路由

| 路由 | 鉴权 | 说明 |
|---|---|---|
| `GET/POST/DELETE /lanes/{lane}/mcp` | access key | MCP 端点 |
| `POST /lanes/{lane}/join` | join key | 机器加入 |
| `GET /lanes/{lane}/connect` | machine credential | 守护进程 WebSocket |
| `DELETE /lanes/{lane}/membership` | machine credential | 机器主动离开 Lane（`lanesh leave`） |
| `POST /transfers/{id}/upload`、`GET /transfers/{id}/download` | 一次性 ticket | 文件传输数据流 |
| `/api/v1/*` | admin token 或会话 cookie | 管理 API |
| `GET /install.sh`、`GET /install.ps1` | 无 | 安装脚本（从 GitHub Releases 下载与 Server 相同版本的二进制） |
| `GET /healthz` | 无 | 健康检查 |
| `/*` | — | Web UI 静态资源 |

### 9.2 配置

| 环境变量 | 默认值 | 说明 |
|---|---|---|
| `LANESH_LISTEN` | `:8080` | 监听地址 |
| `LANESH_DATA_DIR` | `/data`（镜像）/ `./data`（二进制） | SQLite 和其他数据的目录 |
| `LANESH_PUBLIC_URL` | — | 对外访问地址，用于生成接入片段和安装脚本 |
| `LANESH_ADMIN_TOKEN` | 自动生成 | 管理员凭证 |
| `LANESH_TRANSFER_MAX_BYTES` | 不限 | 单次传输上限 |
| `LANESH_AUDIT_RETENTION_DAYS` | `90` | 审计日志保留天数 |

Server 本身只监听 HTTP，TLS 由反向代理（例如 Coolify / Traefik）终止。守护进程拒绝连接非 HTTPS 的 Server，`localhost` 除外。

### 9.3 存储

使用 SQLite（纯 Go 驱动，不依赖 CGO，便于交叉编译）。主要数据表：

- `lanes`：`id`、`name`、`created_at`
- `keys`：`id`、`lane_id`、`kind`（`join` / `access`）、`name`、`hash`、`prefix`、`expires_at`、`created_at`、`last_used_at`、`revoked_at`
- `memberships`：`lane_id`、`machine_id`、`display_name`、`credential_hash`、`os`、`arch`、`hostname`、`version`、`expose_skills`、`allow_exec`、`joined_at`、`last_seen`、`revoked_at`
- `skill_snapshots`：`lane_id`、`machine_id`、`index_json`、`updated_at`（用于离线时在 UI 中展示）
- `skill_history`：`lane_id`、`machine_id`、`hash`、`index_json`、`seq`（每台机器内单调递增的序号，不依赖时钟精度）。每个 (Lane, 机器) 保留最近 20 个 skill 索引版本，用于生成变化报告（§8.4）
- `admin_sessions`：Web UI 登录会话
- `audit_log`：`ts`、`lane_id`、`actor`（access key ID 与 MCP session ID）、`action`、`machine_id`、`detail`、`result`

数据库迁移内置在二进制中，启动时自动执行。

### 9.4 审计

- 记录执行的命令、工作目录、退出码、耗时，文件读写与传输的路径和字节数，以及凭证的发放和吊销。
- 不记录命令输出和文件内容。
- 守护进程在本地 `logs/audit.log` 中也记录一份，Server 被攻破时仍可在机器本地追溯。

## 10. Web 管理界面

### 10.1 功能范围（v1）

- 登录：输入 admin token，换取 HttpOnly、Secure、`SameSite=Strict` 的会话 cookie。登录接口有速率限制。
- Lane：列表、创建、重命名、删除。
- 每个 Lane 下：
  - 机器：在线状态、系统、版本、最后在线时间、策略；移除机器（吊销 machine credential）
  - skill：按机器浏览，渲染 `SKILL.md`，查看文件树；标出不同机器上同名 skill 的内容是否一致
  - 凭证：发放（明文只显示一次）、吊销、最后使用时间
  - 接入片段：各系统的安装加 join 命令；Claude Code、Codex、Cursor 等客户端的 MCP 配置
  - 活动：审计日志，可按机器、动作和凭证筛选
- 实例信息：Server 版本、数据目录、连接数。

只做查看和管理，不提供 Web 终端或在界面中执行命令。

### 10.2 技术

- SvelteKit + `adapter-static`（SPA）+ TypeScript，Svelte 5。
- 样式使用 CSS 变量定义设计令牌，不引入重型组件库。
- 代码检查与格式化使用 Vite/VoidZero 生态的 `oxlint` 和 `oxfmt`；Svelte 编译器诊断和类型检查由 `svelte-check` 负责。
- 国际化：英文为默认，同时提供简体中文。
- 开发时使用 Vite 开发服务器，把 `/api`、`/lanes` 代理到本地 Go Server。
- 项目中的 Svelte 相关 skill 放在 `.agents/skills/`，`.claude/skills/` 下创建指向它们的符号链接。

### 10.3 嵌入方式

- 前端构建输出到 `web/build/app/`，`web/embed.go` 用 `//go:embed all:build` 把它编进二进制，单二进制即可提供完整服务。
- `go:embed` 不能引用上级目录，所以嵌入代码必须放在 `web/` 包内，由 Server 引用。
- 仓库中保留 `web/build/.gitkeep`，确保未构建前端时（例如 `go install`）也能编译；此时 UI 显示“界面未构建”的提示页。构建工具只清理 `web/build/app/`，不会删掉这个占位文件。
- 代价只是二进制体积增加几 MB，以及修改前端后需要重新编译 Go 程序，这两点都可以接受。

### 10.4 视觉与品质标准

- 风格：简约、扁平，以排版、留白和细节取胜，定位为精致的工具型产品，不做大按钮、大海报式的营销页风格。
- 品质目标：达到 Awwwards、Webby Awards、FWA 获奖作品的水准。
- 自查清单：字体层级与节奏、栅格与间距一致性、克制的用色与对比度、细微的过渡与反馈动效、空状态 / 加载 / 错误状态、深色模式、键盘可访问性与焦点样式、响应式布局。
- 流程：每完成一轮，在浏览器中截图，逐条对照清单自查和改进，直到达到标准。

## 11. 安装、服务管理与升级

### 11.1 安装途径

| 途径 | 命令 |
|---|---|
| npm | `npm i -g lanesh` 或 `npx lanesh ...` |
| 安装脚本（Unix） | `curl -fsSL https://<server>/install.sh \| sh` |
| 安装脚本（Windows） | `irm https://<server>/install.ps1 \| iex` |
| 手动 | 从 GitHub Releases 下载对应平台的压缩包或独立二进制 |

所有途径最终都把二进制放到 `~/.lanesh/bin/`。

### 11.2 npm 包

- 只发布一个包 `lanesh`，内容是一个很小的 JS 启动器，不包含二进制，也不使用 postinstall。
- 运行时：如果受管二进制不存在，或版本低于 npm 包的版本，就从 GitHub Releases 下载与 npm 包版本相同的独立二进制（`lanesh_<version>_<os>_<arch>[.exe]`，无需解压），校验 `checksums.txt` 中的 sha256 后放入 `~/.lanesh/bin/`；如果受管二进制版本更高（已经自动升级过），直接使用它。然后把参数原样转交给它执行。
- 这样可以兼容 `--ignore-scripts` 和 `npx`，并且二进制的升级不会与 npm 的文件管理冲突。

### 11.3 后台服务

`lanesh join` 会把守护进程安装为当前用户级别的后台服务：

| 系统 | 机制 | 说明 |
|---|---|---|
| macOS | launchd LaunchAgent（`~/Library/LaunchAgents`） | 登录后自动启动，崩溃后自动重启 |
| Linux | `systemd --user` | 无桌面的服务器需要执行一次 `loginctl enable-linger <user>`（需要 sudo），否则用户退出登录后服务会停止；`lanesh doctor` 会检测并提示 |
| Windows | 用户登录时触发的计划任务（Task Scheduler） | 不使用 Windows Service：Service 默认以 SYSTEM 运行，home 目录和环境都不对 |

必须以当前用户身份运行，否则无法读取该用户的 skill 目录，也拿不到用户的环境。

服务启动的命令是 `~/.lanesh/bin/lanesh daemon`，`LANESH_HOME` 通过服务配置传入。macOS 用 launchd 的 `KeepAlive`，Linux 用 systemd 的 `Restart=always`，进程退出后都会被自动重新拉起。

Windows 有两处特殊处理：

- **不弹控制台窗口**：用户登录时运行控制台程序会出现一个黑窗口，所以计划任务通过 `conhost.exe --headless` 启动 lanesh。
- **退出后重新拉起**：计划任务的"失败时重新启动"只在任务启动失败时生效，不会在进程退出后重启。所以计划任务运行的是 `lanesh daemon --supervise`，它作为守护父进程启动真正的 `lanesh daemon`，子进程退出后按退避策略重新拉起。子进程放在带 `KILL_ON_JOB_CLOSE` 的 Job Object 中，父进程结束时子进程会一起结束。`lanesh stop` 会结束计划任务，并终止记录在 `supervisor.pid` 中的父进程。

守护进程用锁文件 `~/.lanesh/daemon.lock` 保证同一时间只运行一个实例，并把连接状态写入 `~/.lanesh/state.json`，供 `lanesh status` 和 `lanesh doctor` 读取。

守护进程每隔几秒重新读取一次配置文件。`lanesh join`、`lanesh leave`、`lanesh config set` 修改配置后无需重启服务：新增的 Lane 会自动连接，移除的 Lane 会自动断开，策略或机器名变化会触发重新连接。

### 11.4 自动升级

- 守护进程在每次握手（`welcome`）时获得 Server 版本。Server 升级重启后，所有守护进程都会重连，从而拿到新版本号。
- 目标版本 = 所有已连接 Server 中最高的正式版本（不含预发布或开发版本）。只升级，不自动降级。
- 目标版本高于当前版本时：
  1. 从 `https://github.com/jtsang4/lanesh/releases/download/v<version>/` 下载对应平台的独立二进制 `lanesh_<version>_<os>_<arch>[.exe]` 和 `checksums.txt`（直接下载链接，不走 GitHub API，没有限流问题）
  2. 校验 sha256
  3. 原子替换 `~/.lanesh/bin/lanesh`。Windows 上不能覆盖正在运行的 exe，所以先把当前文件改名为 `.old`，再放入新文件，下次启动时删除 `.old`
  4. 以退出码 75 退出进程，由服务管理器（Windows 上是守护父进程）重新拉起
- 只有当守护进程运行的就是受管二进制（`~/.lanesh/bin/lanesh`）时才会自动升级；从开发目录直接运行的守护进程不会替换自己。
- 升级来源可以通过 `[update] source` 配置（便于 fork 或镜像），也可以用 `[update] auto = false` 关闭自动升级。
- `lanesh update` 手动触发，`lanesh update --version <v>` 安装指定版本（允许降级）。
- 镜像中不需要包含客户端二进制。

### 11.5 CLI

| 命令 | 说明 |
|---|---|
| `lanesh server` | 运行 Server |
| `lanesh join <lane-url> --key <join-key> [--name] [--no-exec] [--no-service]` | 加入 Lane，把当前二进制复制到受管目录，安装并启动后台服务 |
| `lanesh leave <lane> [--keep-service]` | 离开 Lane 并通知 Server；没有剩余 Lane 时卸载服务 |
| `lanesh status [--json]` | 服务状态、已加入的 Lane、各连接状态、版本 |
| `lanesh logs [-f]` | 查看守护进程日志 |
| `lanesh start` / `stop` / `restart` | 控制后台服务 |
| `lanesh update [--version] [--check]` | 手动升级到 Server 的版本（或最新发布版本） |
| `lanesh doctor [--refresh-env]` | 诊断 PATH、服务、linger、连通性、版本，刷新执行环境 |
| `lanesh config show` / `set <key> <value>` / `unset <key>` | 查看和修改本地配置与策略 |
| `lanesh admin login <server>` / `logout` | 保存或删除管理员凭证，供下列命令使用 |
| `lanesh lane list` / `create` / `rename` / `delete` | Lane 管理（调用管理 API） |
| `lanesh key list` / `create` / `revoke` | 凭证管理 |
| `lanesh machine list` / `remove` | 查看或移除 Lane 中的机器 |
| `lanesh daemon [--supervise]` | 守护进程本体，由后台服务调用 |
| `lanesh healthcheck` | 请求本地 `/healthz`，供容器健康检查使用 |

## 12. 部署

### 12.1 容器镜像

- 发布到 `ghcr.io/jtsang4/lanesh`，标签为 `<version>`、`<major>.<minor>` 和 `latest`，支持 `linux/amd64` 和 `linux/arm64`。
- 基础镜像使用 distroless static（非 root，UID 65532），镜像中只有一个二进制。`/data` 由 `WORKDIR` 创建并归属该用户，新建的命名卷可以直接写入；如果改用宿主机目录挂载，需要先把目录属主设为 65532。
- 健康检查通过 `HEALTHCHECK CMD ["lanesh", "healthcheck"]` 实现，不依赖镜像中的 curl 或 wget。
- 数据目录 `/data` 需要挂载持久卷。

```yaml
# deployments/docker-compose.yml
services:
  lanesh:
    image: ghcr.io/jtsang4/lanesh:latest
    command: ["server"]
    environment:
      LANESH_PUBLIC_URL: https://lanesh.example.com
      LANESH_ADMIN_TOKEN: ${LANESH_ADMIN_TOKEN}
    volumes:
      - lanesh-data:/data
    ports:
      - "8080:8080"
volumes:
  lanesh-data:
```

在 Coolify 中部署时，配置域名、环境变量和 `/data` 持久卷即可；反向代理需要允许 WebSocket 升级（Coolify 默认支持）。

### 12.2 单二进制

```bash
LANESH_PUBLIC_URL=https://lanesh.example.com lanesh server
```

前端已嵌入二进制，只需要准备好数据目录和反向代理。

## 13. 构建与发布

- **触发**：推送 `v*` 标签。
- **前端**：先构建 `web/`，产物进入 `web/build/app/`，供 `go:embed` 使用。
- **GoReleaser**：
  - 构建 `darwin`、`linux`、`windows` × `amd64`、`arm64`，`CGO_ENABLED=0`
  - 产出压缩包（Windows 为 zip）、独立二进制（供 npm 启动器、安装脚本和自动升级使用）和 `checksums.txt`，发布到 GitHub Releases
  - 构建多架构镜像，推送到 GHCR
- **npm**：独立 job，把启动器包的版本设为标签版本后，通过 npm trusted publishing（GitHub Actions OIDC，无需长期 token）发布，并自动生成 provenance。预发布版本（标签含 `-`）发布到 `next` dist-tag。具体步骤见 `docs/releasing.md`。
- **版本注入**：通过 `-ldflags` 注入版本号、提交哈希和构建时间。
- **CI**：每个 PR 运行 Go 测试（三个平台）、`go vet`、golangci-lint、前端检查与构建、npm 启动器测试、GoReleaser 配置检查，并用快照构建镜像后实际启动容器做冒烟测试。

## 14. 项目结构

参考 golang-standards/project-layout：

```
cmd/lanesh/                # 唯一入口，按子命令分发
internal/
  cli/                     # 子命令定义
  server/                  # HTTP 路由、Lane 与连接管理、管理 API
  mcp/                     # MCP 工具实现与简报令牌检查
  daemon/                  # 守护进程：连接、请求分发
  skills/                  # skill 扫描、去重、监听、哈希
  runner/                  # 任务管理、环形输出缓冲、shell 选择、进程树控制
  files/                   # 文件读写与传输
  protocol/                # 控制协议消息类型与版本
  store/                   # SQLite 访问与迁移
  auth/                    # 凭证生成、哈希、校验
  service/                 # launchd / systemd --user / Windows 计划任务
  update/                  # 自动升级
  version/                 # 构建信息
internal/server/scripts/   # install.sh、install.ps1 模板（由 Server 渲染）
web/                       # SvelteKit 前端与 embed.go
build/
  package/Dockerfile
  npm/                     # npm 启动器包
deployments/               # docker-compose 示例
scripts/                   # dev.sh（本地演示环境）、service-test.sh（服务生命周期测试）
docs/                      # 设计、管理 API、发布流程
.agents/skills/            # 项目级 skill
.claude/skills/            # 指向 .agents/skills 的符号链接
```

主要依赖：MCP 官方 Go SDK（`github.com/modelcontextprotocol/go-sdk`）、`github.com/coder/websocket`、纯 Go 的 SQLite 驱动（`modernc.org/sqlite`）、`github.com/pelletier/go-toml/v2`、`github.com/fsnotify/fsnotify`、`github.com/spf13/cobra`、`golang.org/x/sys`、`golang.org/x/term`。

端到端测试位于 `internal/server/e2e_test.go`：在进程内启动 Server 和多个守护进程，用官方 MCP SDK 客户端走完整链路。

## 15. 安全

### 15.1 威胁模型

- **access key 等同于对 Lane 内所有开放执行的机器的完全控制权**，需要按最高敏感级别保管。泄露后应立即在 Web UI 中吊销。
- **Lane 是信任边界**：Lane 中的 skill 内容会被 Agent 当作指令执行。任意一台提供方机器被攻破，都可以对消费该 Lane 的 Agent 实施提示注入。只把互相完全信任的机器放进同一个 Lane。
- **Server 能看到所有流量的明文**（命令、输出、文件），所以必须自托管在可信环境中。端到端加密不在 v1 范围内。

### 15.2 措施

- 强制 HTTPS（`localhost` 除外），凭证仅存哈希，常量时间比较。
- 各类凭证可单独吊销；machine credential 吊销后立即断开连接。
- 提供方策略在机器本地生效，Server 和消费方都无法越权。
- 守护进程以普通用户身份运行，不提权。
- `read_skill` 拒绝访问 skill 目录以外的路径。
- 登录和加入都有速率限制。只有当请求来自本机或内网地址（即前置反向代理）时，才信任 `X-Forwarded-For` / `X-Real-IP`。
- 每台机器同时最多运行 64 个任务；`job_kill` 和守护进程退出都有超时，脱离进程组的子进程不会让任务永远处于运行状态。
- 某个 Lane 被移除或关闭执行权限时，守护进程会结束该 Lane 正在运行的任务。
- Server 和机器本地双重审计。
- 自动升级只从配置的来源下载，并校验 sha256。

## 16. 路线图

### v1 里程碑

1. **骨架**：项目结构、CLI 框架、构建与 CI
2. **核心通路**：Server（Lane、凭证、加入、WebSocket 连接）+ 守护进程（握手、心跳、重连）+ SQLite
3. **MCP 基础**：`list_machines`、`describe_machine`、`read_skill`，简报令牌检查
4. **执行与文件**：`exec` 与任务管理、`read_file` / `write_file`、`transfer`，三平台适配
5. **运维体验**：后台服务安装与管理、`doctor`、自动升级、npm 启动器、安装脚本
6. **Web UI**：全部管理功能，经过品质自查迭代
7. **发布**：GoReleaser、GHCR 镜像、npm 发布、文档

### v1 之后

- **官方 Skills 扩展（SEP-2640，`io.modelcontextprotocol/skills`）**：在主流宿主支持后评估接入。截至 2026 年 11 月，Claude Code、Codex、Cursor、VS Code 都还不支持，只有调试器和少数客户端部分支持。另外，该扩展默认 skill 在宿主本机使用和执行，没有表达"必须在某台远程机器上执行"的方式；接入时需要设计好执行位置的表达，避免宿主把脚本拿到 Agent 本机执行。

- PTY 与交互式终端（Unix pty / Windows ConPTY）
- 远程 MCP connector 所需的 OAuth
- 跨机器 skill 搜索
- 把 skill 以 MCP resources / prompts 的形式暴露
- 端到端加密、点对点直连传输
- 更细粒度的策略（命令白名单、路径白名单）

## 17. 已知限制

- Server 只支持单实例运行。
- 只消费、未运行守护进程的机器不能作为文件传输的目标。
- 守护进程重启会终止所有正在运行的任务。
- 只有正式发布的版本才会触发客户端自动升级；Server 运行开发版本时，客户端不会自动升级。
