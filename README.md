# CPA 自用版

这是基于 [router-for-me/CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI) 的自用构建，重点服务 Codex/Responses 稳定性、多账号运行、NAS/Docker 部署和日常 CPA 管理。

当前同步基线：上游 `v7.2.5` / `origin/main`，自用版本建议标记为：

```text
v7.2.5-selfuse.20260615
```

## 本构建改动

### 1. Codex 上下文过长直接交回客户端

当 Codex 上游以 `context_too_large` / `context_length_exceeded` 结束流式响应时，本构建不再在 CPA 中间层自行压缩历史、生成 `history.txt` 或移除 reasoning 后继续重试。

行为：

- 保留上游式错误，直接返回给客户端处理。
- 避免 CPA 把历史会话改写成新的请求后再次喂给模型。
- 降低长会话里重复读工作区、重复确认状态、重复规划的风险。
- SSE 行破损修复仍然保留，二者是不同问题。

### 2. 加密 reasoning 上下文降级重试

部分 Codex/Responses 请求会携带 `input[*].encrypted_content`。当上游明确拒绝这段加密 reasoning 上下文时，本构建会移除无效的加密 reasoning 上下文，并重试一次。

效果：

- 减少 `encrypted_content` 被上游拒绝导致的失败。
- 当上游返回 `Item with id 'rs_...' not found` 且提示 `store=false` 时，移除 stale reasoning item 并重试一次。
- 提升对复用 Responses reasoning 上下文客户端的兼容性。
- 重试只针对明确的 reasoning 上下文拒绝或丢失场景。

### 3. Codex 响应头超时

Codex 上游请求有时会在返回响应头前卡住。此时 CPA 无法判断上游是在排队、停滞，还是 HTTP/2 连接卡死。

本构建增加只作用于响应头阶段的超时：

```yaml
codex-response-header-timeout-seconds: 180
```

行为：

- 只限制等待上游响应头的时间。
- 响应头到达后的流式正文不受该超时限制。
- 超时后进入现有重试和账号重选流程。
- 避免一次卡死的上游请求占住客户端很多分钟。

设置为负数可关闭该超时：

```yaml
codex-response-header-timeout-seconds: -1
```

也支持环境变量覆盖：

```bash
CPA_CODEX_RESPONSE_HEADER_TIMEOUT_SECONDS=180
```

### 4. 流式 keepalive 和启动阶段重试

建议配合以下流式保活和启动阶段重试配置使用：

```yaml
nonstream-keepalive-interval: 15
streaming:
  keepalive-seconds: 15
  bootstrap-retries: 1
```

行为：

- 下游客户端等待时可以收到 keepalive 事件。
- 首个流式 payload 前失败时可以安全重试。
- 上下文过长的免费账号重试会被限制，避免反复落到不可用账号。
- 降低慢上游启动导致的客户端误断开。

### 5. 管理 UI 增强

管理页保留 selfuse 的运维增强：

- 可视化配置 `codex-response-header-timeout-seconds`。
- auth 文件单独测试模型。
- 当前页批量测试 auth 文件。
- 每个账号显示测试结果和延迟。

### 6. OpenAI-compatible 上游 JSON 预检

Kimi K2.7 Code 等走 `openai-compatibility` 的模型在请求体包含未转义反斜杠时，上游可能返回 Cloudflare 侧的 `invalid escaped character in string`。本构建会在入口路由前和发往 OpenAI-compatible 上游前都对 JSON 做兼容处理：

- 对 `C:\Users\...` 这类常见未转义 Windows 路径，自动修复字符串里的非法反斜杠转义后继续请求。
- `/v1/chat/completions` 和 `/v1/completions` 会先修复/校验请求体，再读取 `model` 做 provider 路由，避免可修复请求被误判成 `unknown provider` 并快速返回 `502`。
- 对缺引号、结构损坏等不可恢复的非法 JSON，仍然在 CPA 本地返回 `400`，错误信息带有具体解析位置。
- 不再把不可恢复的破损请求发给 Cloudflare/OpenAI-compatible 上游。
- 流式和非流式 chat completion 路径都覆盖。

## 上游同步摘要

本轮合并了 `v7.1.76` 之后到 `v7.2.5` 的上游更新，重点包括：

- `v7.2.0`：增强 KV 缓存容错，移除已废弃的 AMP 集成。
- `v7.2.1`：插件 host 增加认证回调，社区插件能力继续增强。
- `v7.2.2`：新增 OpenAI video 支持和响应归一化。
- `v7.2.3`：新增 XAI WebSocket executor。
- `v7.2.4`：新增 WebSocket transcript 状态跟踪和 compaction trigger 支持。
- `v7.2.5`：新增 video authentication binding，并增加 config API key exclusion 管理。

上游已经覆盖的通用修复尽量使用官方实现；上游尚未覆盖的 selfuse 运行补丁继续保留。

## 推荐配置

示例运维配置：

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

## Docker Compose 使用

构建并启动：

```bash
docker compose up -d --build
```

如果环境需要代理：

```bash
export HTTP_PROXY=http://<proxy-host>:<proxy-port>
export HTTPS_PROXY=http://<proxy-host>:<proxy-port>
export NO_PROXY=localhost,127.0.0.1,<lan-host>
docker compose up -d --build
```

管理页和 API 端口取决于你的 compose 文件。参考部署中：

```text
CPA API:      http://<host>:8317
CPAMC 代理:   http://<host>:18317/management.html
```

## 版本规则

本仓库的自用发布版本固定使用 `selfuse` 后缀，例如：

```text
v7.2.5-selfuse.20260615
```

NAS 本地 Docker 镜像建议使用稳定标签：

```text
cli-proxy-api:v7.2.5-selfuse.20260615
```

这样日志、镜像、Release 和回滚点都能保持清晰。

## 安全说明

不要提交真实 auth 文件、refresh token、access token、id token、management key 或 API key。

推荐作为运行态文件保留在仓库外或 `.gitignore` 中：

```text
auth-dir/
auths/
logs/
*.sqlite
*.db
config.yaml
```

公开 fork 或发布前，建议扫描敏感信息：

```bash
rg -n "github_pat_|refresh_token|access_token|id_token|sk-[A-Za-z0-9]|secret-key:" .
```

## 上游

本仓库基于：

- 上游项目：[router-for-me/CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI)
- 许可证：MIT，见 [LICENSE](LICENSE)

通用的小修复应尽量单独提交给上游。本仓库保留的本地运维改动，多数与 selfuse 部署和管理需求有关。
