## [2026-09-18 10:42] | Task: native host 扩展身份白名单一致性

### 用户问题

Chrome 当前运行的是仓库的 Load unpacked fork 扩展，但 Native Messaging host
manifest 只登记了 Web Store 扩展 ID，导致 `connectNative` 在 Chrome 侧被拒绝，
随后 CLI 报告找不到 active socket。

### 变更

- `cmd/open-browser-use/main.go`
  - 把 Web Store、beta keyed 和 fork Load unpacked 三种内置扩展 ID 收口到同一
    `nativeMessagingExtensionIDs` 来源。
  - 默认生成的 Native Messaging manifest 同时允许这三种内置 ID，解决 CLI 默认
    setup/install-manifest 与实际 fork 扩展身份失配。
  - `--extension-id` 仍可用于自定义扩展，并保持单 ID 精确白名单。
  - profile 探测复用同一组内置 ID，避免探测与连接配置再次分叉。
- `cmd/open-browser-use/main_test.go`
  - 更新默认 manifest 测试，覆盖全部内置 origins。
  - 增加显式自定义 ID 保持窄白名单的回归测试。
- `docs/ARCHITECTURE.md`、`docs/SECURITY.md`
  - 记录多身份默认白名单与显式自定义 ID 的边界。

### 验证

- `go test ./cmd/open-browser-use`
- `go test ./...`
