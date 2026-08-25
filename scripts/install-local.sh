#!/usr/bin/env bash

# 把当前仓库的 Go CLI 构建并安装为本地 obu，替换全局安装的版本。
# 用法: ./scripts/install-local.sh
# 需要: go 在 PATH 中; ~/.local/bin 在 PATH 中且优先于其他 obu 入口。
#
# 覆盖三处:
#   1. ~/.local/bin/open-browser-use (go build 产物)
#   2. ~/.local/bin/obu (symlink)
#   3. Chrome native host 的 stable link (macOS/Linux)
#
# 恢复全局 npm 版本: 删除 ~/.local/bin 下两个文件, 再把 stable link
# 指回 npm 包内的 native 二进制即可。

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
install_dir="${HOME}/.local/bin"
binary_path="${install_dir}/open-browser-use"

# 1. 构建
(cd "${repo_root}" && go build -o "${binary_path}" ./cmd/open-browser-use)

# 2. obu symlink
ln -sf open-browser-use "${install_dir}/obu"

# 3. Chrome native host stable link (extension 启动 host 时走这里)
case "$(uname -s)" in
  Darwin)
    host_link="${HOME}/Library/Application Support/OpenBrowserUse/native-host/open-browser-use"
    ;;
  Linux)
    host_link="${HOME}/.local/share/open-browser-use/native-host/open-browser-use"
    ;;
  *)
    host_link=""
    ;;
esac
if [[ -n "${host_link}" ]]; then
  mkdir -p "$(dirname "${host_link}")"
  ln -sf "${binary_path}" "${host_link}"
fi

# 4. 快速验证
echo "✅ installed: $(command -v obu) -> $(readlink "$(command -v obu)")"
"${binary_path}" version
if "${binary_path}" mcp >/dev/null 2>&1; then
  echo "⚠️  MCP 仍然暴露在 CLI 中"
else
  echo "✅ mcp 未暴露 (预期)"
fi