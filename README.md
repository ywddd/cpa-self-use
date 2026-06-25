# CPA 自用部署仓库

这是基于 [router-for-me/CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI) 维护的自用分支，主要用于 NAS / Docker 环境下运行 CLIProxyAPI 和 CPA 管理界面。

本仓库偏向个人部署与日常运维，不建议把真实配置、账号 token、管理密钥或 API key 提交到仓库。

## 当前版本

当前同步基线：上游 `v7.2.37`。

自用版本建议标记为：

```text
v7.2.37-selfuse.20260625
```

## 自用保留改动

本分支在上游基础上保留了一些面向 Codex、Responses、多账号和 NAS 部署的自用改动。

### 1. Codex 上下文过长处理

当 Codex 上游返回 `context_too_large`、`context_length_exceeded` 等错误时，本分支倾向于把错误交回客户端处理，而不是在 CPA 中间层强行压缩历史、生成 `history.txt` 或移除 reasoning 后继续重试。

这样可以避免长会话中出现重复读取工作区、重复确认状态、重复规划任务等问题。

### 2. 加密 reasoning 降级重试

部分 Codex / Responses 请求会携带 `input[*].encrypted_content`。当上游明确拒绝这类加密 reasoning 上下文时，本分支会移除无效的加密 reasoning 上下文，并重试一次。

当上游返回 `Item with id 'rs_...' not found` 且提示 `store=false` 时，也会移除 stale reasoning item 后重试一次。

### 3. Codex 响应头超时

Codex 请求有时会卡在响应头阶段。本分支支持只作用于“等待响应头”的超时配置：

```yaml
codex-response-header-timeout-seconds: 180
```

响应头到达后的流式正文不受这个超时限制。

关闭该超时：

```yaml
codex-response-header-timeout-seconds: -1
```

也可以通过环境变量覆盖：

```bash
CPA_CODEX_RESPONSE_HEADER_TIMEOUT_SECONDS=180
```

### 4. OpenAI-compatible JSON 预检

Kimi、OpenAI-compatible 等入口在遇到包含未转义反斜杠的请求体时，上游可能返回 `invalid escaped character in string`。

本分支会在路由前和转发前做兼容处理：

- 常见 Windows 路径中的非法反斜杠会被修复后继续请求。
- `/v1/chat/completions` 和 `/v1/completions` 会先修复/校验请求体，再读取 `model` 做 provider 路由。
- 缺引号、结构损坏等不可恢复的非法 JSON 仍会在本地返回 `400`。

### 5. 管理界面增强

自用管理界面保留以下运维增强：

- 可视化配置 `codex-response-header-timeout-seconds`。
- 单独测试 auth 文件。
- 当前页批量测试 auth 文件。
- 每个账号展示测试结果和延迟。

## 推荐配置

以下配置适合 NAS 上的长期自用服务，可按实际情况调整：

```yaml
request-retry: 3
max-retry-credentials: 3
max-retry-interval: 30

routing:
  session-affinity: true

nonstream-keepalive-interval: 15
codex-response-header-timeout-seconds: 180

streaming:
  keepalive-seconds: 15
  bootstrap-retries: 1
```

## NAS / Docker 部署

### 目录建议

建议把运行态文件放在仓库外，或至少不要加入 Git：

```text
/volume1/docker/cpa/config.yaml
/volume1/docker/cpa/auth-dir/
/volume1/docker/cpa/plugins/
/volume1/docker/cpa/cpa-manager-data/
```

### 启动服务

在部署目录执行：

```bash
docker compose up -d --build
```

查看服务状态：

```bash
docker compose ps
```

查看日志：

```bash
docker compose logs -f --tail=200 cli-proxy-api
```

### 端口说明

端口以你的 `docker-compose.yaml` 为准。常见自用部署中：

```text
CPA API:  http://<host>:8317
管理界面: http://<host>:18317/management.html
```

## 更新流程

推荐流程：

```bash
git fetch origin main
git checkout main
git pull --ff-only origin main
docker compose up -d --build
```

如果 NAS 上还有历史构建目录或缓存，可以在确认当前版本可用后清理：

```bash
rm -rf build-v* .gocache .gomodcache tmp-*
```

不要删除正在挂载使用的目录，例如 `config.yaml`、`auth-dir/`、`plugins/`、`cpa-manager-data/`。

## 安全注意事项

不要提交以下内容：

```text
auth-dir/
auths/*.json
logs/
*.sqlite
*.db
config.yaml
docker-compose.yaml
.env
*.bak
```

不要把以下真实值写进仓库：

- API key
- management key
- access token
- refresh token
- id token
- cookie
- 私钥或云厂商凭证

提交前建议先扫一遍：

```bash
rg -n -I "github_pat_|ghp_|refresh_token|access_token|id_token|sk-[A-Za-z0-9]|secret-key|BEGIN .*PRIVATE KEY" .
```

如果发现密钥已经提交到公开仓库，应立即：

1. 撤销或轮换对应密钥。
2. 删除当前分支里的敏感文件。
3. 如有必要，再重写 Git 历史。

## 常用排查命令

检查容器：

```bash
docker compose ps
```

查看 API 日志：

```bash
docker compose logs -f --tail=200 cli-proxy-api
```

重启服务：

```bash
docker compose restart cli-proxy-api cpa-manager cpa-manager-proxy
```

检查当前 Git 版本：

```bash
git status --short --branch
git log --oneline -5
```

检查 README 是否为可读 UTF-8：

```bash
file README.md
sed -n '1,80p' README.md
```

## 说明

本仓库是自用部署仓库，优先保证 NAS 上的稳定运行和维护便利。上游已经覆盖的通用修复尽量使用官方实现；上游尚未覆盖的自用运行补丁会继续保留。
