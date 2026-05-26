# CPA Self-Use

Personal build of [router-for-me/CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI), focused on Codex/Responses stability, multi-account operation, and day-to-day CPA management.

This repository keeps the upstream MIT license and upstream project attribution. It is not an official upstream release; it is a practical self-use branch with fixes and operational UI additions that may be useful for similar CPA deployments.

## What This Build Changes

### 1. Encrypted Reasoning Retry

Some Codex/Responses requests can include encrypted reasoning context in `input[*].encrypted_content`. When the upstream rejects that encrypted reasoning payload, the original CPA behavior may fail the request directly.

This build detects that rejection, removes the invalid encrypted reasoning context, and retries the request once.

Result:

- fewer failures from upstream `encrypted_content` rejection
- better compatibility with clients that reuse Responses reasoning context
- retry is scoped to this specific upstream rejection pattern

### 2. Reasoning / Thinking Compatibility

This build improves compatibility for requests that enable thinking or reasoning effort.

Result:

- fewer direct failures when clients send reasoning-related fields
- better handling for Codex/Responses style reasoning payloads
- cleaner behavior when model/client formats differ

### 3. Codex Response-Header Timeout

Codex upstream requests can sometimes hang before response headers are returned. In that state CPA cannot know whether upstream is queued, stalled, or the HTTP/2 connection is stuck.

This build adds a response-header-only timeout:

```yaml
codex-response-header-timeout-seconds: 180
```

Behavior:

- only limits the time spent waiting for upstream response headers
- does not limit the streaming body after headers arrive
- after timeout, the failure enters the existing retry/account-reselection flow
- prevents one stuck upstream attempt from holding the client for many minutes

Set a negative value to disable:

```yaml
codex-response-header-timeout-seconds: -1
```

Environment override is also supported:

```bash
CPA_CODEX_RESPONSE_HEADER_TIMEOUT_SECONDS=180
```

### 4. Streaming Keepalive and Bootstrap Retry

This build is intended to run with streaming keepalive and bootstrap retry enabled:

```yaml
nonstream-keepalive-interval: 15
streaming:
  keepalive-seconds: 15
  bootstrap-retries: 1
```

Behavior:

- downstream clients receive keepalive events while waiting
- failures before the first streamed payload can retry safely
- reduces accidental client disconnects during slow upstream startup

### 5. Auth File Model Test Controls

The management UI is enhanced with auth-file testing controls.

Added controls:

- per-auth-file `Test Model` button
- current-page batch test button
- result badge per account:
  - account valid
  - account invalid

The test endpoint sends a minimal model call with the selected auth file pinned, then reports success/failure and latency.

### 6. Visual Config Control for Codex Timeout

The management visual editor now exposes the Codex response-header timeout setting directly.

Added visual field:

```yaml
codex-response-header-timeout-seconds
```

This avoids switching to raw YAML editing for the most common operational tuning.

### 7. Domestic Docker Build Adjustments

This build has Docker-related changes intended for environments where direct Docker Hub access is unreliable.

Practical notes:

- image sources can be changed to reachable mirrors
- build/pull commands can use an HTTP proxy
- can be used with an HTTP proxy:

```text
http://<proxy-host>:<proxy-port>
```

### 8. CPA Manager Proxy Injection

For deployments that use a separate CPAMC container, this setup can run a lightweight manager proxy that injects the custom UI enhancements into CPAMC.

Typical topology:

```text
client/browser
  -> cpa-manager-proxy :18317
  -> cpa-manager

client/API
  -> cli-proxy-api :8317
```

The proxy keeps CPAMC usable while adding local custom controls without rebuilding the CPAMC frontend itself.

## Recommended Config

Example operational settings:

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

## Docker Compose Usage

Build and start:

```bash
docker compose up -d --build
```

If the environment needs a proxy:

```bash
export HTTP_PROXY=http://<proxy-host>:<proxy-port>
export HTTPS_PROXY=http://<proxy-host>:<proxy-port>
export NO_PROXY=localhost,127.0.0.1,<lan-host>
docker compose up -d --build
```

Management/API ports depend on your compose file. In the reference deployment:

```text
CPA API:        http://<host>:8317
CPAMC proxy:    http://<host>:18317/management.html
```

## Security Notes

Do not commit real auth files, refresh tokens, access tokens, management keys, or API keys.

Recommended ignored/runtime-only paths:

```text
auth-dir/
auths/
logs/
*.sqlite
*.db
config.yaml
```

Before making a fork public, scan for secrets:

```bash
rg -n "github_pat_|refresh_token|access_token|id_token|sk-[A-Za-z0-9]|secret-key:" .
```

## Upstream

This repository is based on:

- upstream: [router-for-me/CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI)
- license: MIT, see [LICENSE](LICENSE)

When possible, small generic fixes should be proposed upstream separately. Local operational changes in this repository are kept here because they are deployment-specific.

## Status

This is a self-use build. It is maintained for practical operation first:

- Codex/Responses compatibility
- multi-account retry behavior
- observable account testing
- NAS/Docker deployment
- management UI convenience

No compatibility guarantee is provided beyond the current deployment needs.
