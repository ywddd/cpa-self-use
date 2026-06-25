# CPA 自用分支

这是基于 [router-for-me/CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI) 维护的自用分支，用于保留个人场景中的兼容性补丁和运行增强。

## 当前基线

上游基线：`v7.2.37`

自用版本：`v7.2.37-selfuse.20260625`

## 改动概要

### Codex 上下文过长处理

当上游返回 `context_too_large`、`context_length_exceeded` 等错误时，该分支将错误交回客户端处理，不在中间层压缩或改写历史后继续重试。

### 加密 reasoning 降级重试

当上游拒绝 `input[*].encrypted_content`，或返回 stale reasoning item 相关错误时，该分支会移除无效 reasoning 上下文并重试一次。

### Codex 响应头超时

该分支保留 `codex-response-header-timeout-seconds` 配置，用于限制等待 Codex 响应头的时间。响应头到达后的流式正文不受该超时限制。

### OpenAI-compatible JSON 预检

该分支对部分 OpenAI-compatible 输入中的 JSON 转义问题做入口预检和兼容处理。

### 管理界面增强

该分支保留 auth 文件测试、批量测试和账号测试结果展示等管理界面增强。

## 上游关系

- 通用修复来自上游实现。
- 自用运行补丁保留在该分支。
- 上游合并记录保留在 Git 历史中。

## 许可证

本仓库继承上游项目的许可证。详情见 [LICENSE](LICENSE)。
