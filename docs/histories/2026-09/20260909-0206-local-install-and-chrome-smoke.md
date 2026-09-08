# 本地 CLI 即时交付与真实 Chrome 验收

## 用户诉求

执行真机验证；以后每次修改完成，用户直接运行 obu 即可，不再手工构建或更新。

## 改动

- install-local 同时更新本地 bin 与当前仓库 CLI 包的本机平台二进制，解决 npm link
  入口优先于本地 bin 时仍执行旧二进制的问题；补安装目录创建与复制一致性检查。
- 协作约定明确由 Agent 完成安装、PATH/链接检查、新版行为验证，worktree 交付须
  更新实际生效入口。未修改 shell PATH、扩展或其他仓库。

## 真机验证

通过当前 PATH 的 obu 执行 taskinit 与 exec，连接单一已运行 Chrome target。
本地 HTTP fixture、专用新标签页，验证文件名/argv、AX tree、JS 点击及状态、
Network.responseReceived + loadingFinished + getResponseBody、wait 超时后继续调用。
全部通过；finalize 报告关闭唯一测试 tab，无保留/释放项。本地 HTTP server 已关闭。
用户原有标签页未操作；例行合成测试无昂贵站点经验，不写入 site memory。

## 收尾验证

- `make local-install` 已执行，当前 PATH 的 obu 新版 exec 帮助契约验证通过。
- 当前仓库包内本机二进制与本地 bin 的 SHA-256 一致。
- `bash -n scripts/install-local.sh`、`make check-repo`、`git diff --check` 通过。
- 用户要求提交收尾；保留其他任务工作区，不额外安装、推送或发布。

## 边界

未推送、发布或重载扩展；已运行 native host 不因链接更新而自动重启。本轮未修改
host/extension 行为，因此此次客户端验收继续使用已运行 host。
