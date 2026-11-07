# 发布流程

本文档面向维护者，说明如何发布 Lanesh，以及发布链路首次使用前需要完成的一次性设置。

## 发布产物

推送 `v*` 标签后，`.github/workflows/release.yml` 会依次完成：

1. **GoReleaser**（`goreleaser` job）
   - 构建 `darwin` / `linux` / `windows` × `amd64` / `arm64` 的二进制，前端已嵌入
   - 在 GitHub Releases 发布：
     - 压缩包 `lanesh_<version>_<os>_<arch>.tar.gz`（Windows 为 `.zip`），供手动下载
     - 独立二进制 `lanesh_<version>_<os>_<arch>[.exe]`，供 npm 启动器、安装脚本和自动升级使用
     - `checksums.txt`（sha256）
   - 推送多架构镜像 `ghcr.io/jtsang4/lanesh`，标签为 `<version>`；正式版本额外打 `<major>.<minor>` 和 `latest`
2. **npm**（`npm` job，依赖上一步成功）
   - 把 `build/npm/package.json` 的版本设为标签版本
   - 通过 npm trusted publishing 发布 `lanesh`，自动附带 provenance
   - 预发布版本（例如 `v0.2.0-rc.1`）发布到 `next` dist-tag，不影响 `latest`

npm 启动器按 `lanesh_<version>_<os>_<arch>[.exe]` 的规则下载二进制。修改 `.goreleaser.yaml` 中的命名时，必须同步修改 `build/npm/lib/launcher.js`。

## 一次性设置

### npm trusted publishing

trusted publishing 只能在 npmjs.com 上**已经存在的包**的设置页中配置，所以第一个版本需要手动发布：

1. 按下文“发布步骤”推送第一个标签。GoReleaser job 会成功；npm job 会因为还没配置 trusted publisher 而失败，这是预期行为。
2. 在本地手动发布同一版本（需要已登录 npm 并开启 2FA）：

   ```sh
   cd build/npm
   npm version <version> --no-git-tag-version   # 例如 0.1.0，不带 v
   npm publish --access public --provenance=false   # provenance 只能在 CI 中生成
   git checkout package.json                    # 不提交版本号改动
   ```

3. 在 npmjs.com 打开 `lanesh` 的 **Settings → Trusted Publisher**，选择 GitHub Actions，填写：
   - Organization or user：`jtsang4`
   - Repository：`lanesh`
   - Workflow filename：`release.yml`
   - Environment name：留空
   - Allowed actions：勾选 **Allow npm publish**（不勾选时只允许 `npm stage publish`，而工作流使用的是直接发布）；`npm dist-tag` 不需要
4. **注意有效期**：新建的 trusted publisher 配置必须在 **2 天内**完成一次成功发布，否则会过期，只能删除后重建。所以建议在准备发下一个版本之前再做第 3 步，或者配置好后尽快发一个补丁版本。
5. 确认自动发布正常后，在包设置中选择 **Require two-factor authentication and disallow bypass 2fa tokens**，禁止使用传统 token 发布。

> 本仓库已经完成以上设置：`lanesh` 包已存在，trusted publisher 指向 `jtsang4/lanesh` 的 `release.yml`。

要求：npm CLI ≥ 11.5.1、Node ≥ 22.14.0。工作流使用 Node LTS，并在发布前升级到最新版 npm。

### GHCR 镜像可见性

首次推送镜像后，到 GitHub 个人主页的 **Packages → lanesh → Package settings** 确认：

- 可见性为 **Public**，否则 Coolify 等平台匿名拉取会失败
- 已关联 `jtsang4/lanesh` 仓库。镜像带有 `org.opencontainers.image.source` 标签，通常会自动关联

## 发布步骤

1. 确认 `main` 上的 CI 全部通过。
2. 确认 `docs/design.md`、`README.md` 与本次发布的内容一致。
3. 打标签并推送：

   ```sh
   git switch main && git pull
   git tag -a v0.1.0 -m "v0.1.0"
   git push origin v0.1.0
   ```

4. 在 Actions 中关注 **Release** 工作流，确认以下几项：
   - GitHub Release 中包含全部压缩包、独立二进制和 `checksums.txt`
   - `docker pull ghcr.io/jtsang4/lanesh:<version>` 可以拉取，`amd64` 和 `arm64` 都有
   - `npx lanesh@<version> version` 输出正确的版本号
5. 编辑 GitHub Release 的说明。说明会根据 Conventional Commits 自动生成，可在此基础上补充重点。

## 本地预演

```sh
make snapshot
```

这会构建前端，再用 GoReleaser 生成快照版本的全部产物，输出到 `dist/`。快照模式不会发布任何东西；构建镜像需要本机 Docker 能访问 `gcr.io`。CI 中的 **Container image** job 每次推送都会做同样的快照构建，并对镜像做冒烟测试。

## 版本规则

- 遵循语义化版本，标签格式为 `vMAJOR.MINOR.PATCH`，预发布为 `vMAJOR.MINOR.PATCH-rc.N`。
- 1.0 之前，次版本号的升级可能包含不兼容变更。
- 控制协议的版本号独立于产品版本，规则见 `docs/design.md` §6.3。
