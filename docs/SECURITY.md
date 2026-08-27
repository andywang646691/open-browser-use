# Security Policy

Open Browser Use connects automation clients to the user's real Chrome profile through a Chrome extension, Chrome Native Messaging, and a local native host. Treat it as local browser control infrastructure, not as a sandbox boundary.

## Supported Scope

Security fixes are expected to cover the current Chrome route:

- MV3 Chrome extension under `apps/chrome-extension/`.
- Go native host and CLI under `cmd/open-browser-use/` and `internal/`.
- JavaScript and Python SDK packages under `packages/`.
- Installer, manifest, release, and skill documentation that affects how users enable browser control.

Experimental references, reverse-engineering notes, and archived planning material are useful context, but they are not independently supported interfaces unless they are wired into the current route.

## Reporting Vulnerabilities

If you find a security issue, do not open a public issue with exploit details. Report it privately to the maintainers first.

Please include:

- A short description of the impact.
- Affected component and version, commit, or release.
- Reproduction steps or proof of concept, with secrets and personal browsing data removed.
- Whether the issue requires local access, extension installation, a malicious web page, or a malicious automation client.

Do not include real credentials, private cookies, personal browsing history, or unredacted local paths in reports. Maintainers should coordinate disclosure timing before publishing technical details.

## Security Boundaries

The Chrome route intentionally exposes powerful browser automation primitives to local callers that can reach the Open Browser Use socket.

- The SDKs and CLI do not implement a Codex-style site policy, command allowlist, or user approval workflow.
- Upper-layer runtimes are responsible for deciding which sites, methods, downloads, clipboard operations, and destructive actions are allowed.
- The native host and extension are not a general-purpose sandbox for untrusted code.
- Any integration that accepts remote instructions must add its own authentication, authorization, audit logging, and confirmation policy before invoking Open Browser Use.

## 隔离窗口后台唤醒 (Background Wake)

隔离窗口的会话 tab 被遮挡时，浏览器 compositor 会节流/挂起其渲染与懒加载管线
(rAF + IntersectionObserver)，`document.visibilityState` 表现由窗口遮挡驱动。
OBU 对这些 tab 在**首次 debugger attach** 时自动应用 CDP 层唤醒，并在**主 frame
导航**时自动重放：

- `Emulation.setFocusEmulationEnabled(true)`：页面的 `document.hasFocus()` 恒为
  true。只在隔离窗口会话 tab 上生效，user 模式（共享用户窗口）不生效；
  不抢真实焦点。
- `Page.setWebLifecycleState({state:"active"})`：解除 renderer 对 occluded
  窗口的节流，让 IO/rAF 驱动的懒加载恢复。

真机 A/B（2026-08-27 同机同 Chrome）确认：两条命令组合还会把被遮挡页面的读取
翻为 visible（`visibilityState`→visible、`document.hidden`→false、
`hasFocus`→true）——可见性门控站点（x.com）懒加载从卡死 4 篇恢复为滚动增长
（4→11、HomeTimeline 请求命中）；无唤醒对照保持 hidden+卡死。

两条命令 best-effort、逐命令容错（单个失败不阻断 attach），状态随
`chrome.debugger` detach 自动复位（页面回到真实节流/遮挡状态），不残留。
唤醒只改变页面报告/执行的节流状态，不触碰用户数据，也不读取或修改
cookie/凭据。

## Local Transport

The Go native host bridges Chrome Native Messaging stdio to a local Unix socket.

- The default socket registry is under `/tmp/open-browser-use/`.
- `active.json` is written with `0600` permissions.
- Socket files are created with the current user's local permissions.
- The CLI may scan the socket directory when the active registry is missing or stale, so callers should treat access to the user's local account as access to the browser automation channel.

Planned hardening includes client tokens, peer validation, safer stale-socket handling, and security-focused failure-path tests.

## Chrome Extension Permissions

The MV3 extension uses high-privilege Chrome APIs because browser control requires them:

- `chrome.debugger` for CDP access.
- `tabs` and `tabGroups` for tab and session management.
- `history` for history lookup.
- `downloads` for download observation.
- Native Messaging to communicate with the local host.

Users must understand that installing and enabling the extension allows Open Browser Use to inspect and operate real Chrome tabs in their active profile. The native messaging host manifest must keep `allowed_origins` restricted to the expected Open Browser Use extension ID.

## Clipboard, Downloads, And Files

Clipboard, download, and file chooser features are sensitive.

- Clipboard helpers run through the controlled page context and should only be called for explicit user workflows.
- Download paths and file chooser paths can reveal private filesystem structure; do not log them unless needed, and redact them from shared reports.
- Integrations should confirm uploads, downloads, file selection, form submission, purchases, deletion, and external message sending before execution.

## Secrets And Data Handling

- Never commit credentials, cookies, API keys, browser profile data, native host manifests with private machine-specific paths, or captured user data.
- Keep examples synthetic and minimal.
- Redact local usernames, home directories, tokens, session IDs, and private browsing history from docs, history entries, release notes, and issue reports.
- Prefer environment variables or local config files ignored by Git for development secrets.

## Supply Chain

Repository-level dependency, SBOM, provenance, and release artifact expectations live in `docs/SUPPLY_CHAIN_SECURITY.md`. Security-sensitive dependency or release changes should update that document in the same change when expectations move.

## Maintainer Checklist

For security-affecting changes, verify:

- Manifest origins remain constrained to the intended extension.
- Native host install paths and generated manifests do not leak private local state.
- Socket files, registries, temp files, and release artifacts use the intended permissions.
- Tests cover authorization, stale transport state, and failure paths where practical.
- User-facing setup docs describe the real permissions and risks.
