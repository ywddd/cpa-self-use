# CPA 自用分支

这是基于 [router-for-me/CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI) 维护的自用分支，用于保留个人场景中需要的兼容性补丁和运行增强。

本仓库不是上游项目的通用替代品。通用能力、公开文档和完整使用说明请以原项目为准。

## 当前基线

当前同步基线：上游 `v7.2.37`。

自用版本标记：

```text
v7.2.37-selfuse.20260625
```

## 保留改动概要

### Codex 上下文过长处理

当上游返回 `context_too_large`、`context_length_exceeded` 等错误时，本分支倾向于把错误交回客户端处理，而不是在中间层强行压缩或改写历史后继续重试。

### 加密 reasoning 降级重试

当上游明确拒绝 `input[*].encrypted_content`，或返回 stale reasoning item 相关错误时，本分支会移除无效 reasoning 上下文并重试一次。

### Codex 响应头超时

保留 `codex-response-header-timeout-seconds` 配置，用于限制等待 Codex 响应头的时间。响应头到达后的流式正文不受该超时限制。

### OpenAI-compatible JSON 预检

对部分 OpenAI-compatible 请求体中的常见 JSON 转义问题做入口预检和兼容处理，避免可恢复的格式问题直接传递到上游。

### 管理界面增强

保留若干面向自用运维的管理界面增强，包括 auth 文件测试、批量测试和账号测试结果展示等。

## 与上游的关系

- 上游已经覆盖的通用修复优先采用官方实现。
- 上游尚未覆盖、但对自用环境有价值的补丁会继续保留。
- 合并上游时尽量减少无关改动，避免偏离主线过远。

## 安全说明

不要向仓库提交真实运行态文件或凭证，包括但不限于：

- API key
- management key
- access token
- refresh token
- id token
- cookie
- 私钥或云厂商凭证
- `config.yaml`
- `.env`
- 数据库文件
- auth 文件

如发现敏感信息被提交到公开仓库，应立即撤销或轮换对应凭证，并按需要清理仓库内容。

## 许可证

本仓库继承上游项目的许可证。详情见 [LICENSE](LICENSE)。
