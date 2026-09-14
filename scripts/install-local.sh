#!/usr/bin/env bash

# 把当前仓库的 Go CLI 构建并安装为本地 obu，替换全局安装的版本。
# 用法: ./scripts/install-local.sh
# 需要: go 在 PATH 中。同时更新本仓库链接包的当前平台二进制，
# 兼容 npm link 入口优先于 ~/.local/bin 的开发环境。
#
# 更新位置:
#   1. ~/.local/bin/open-browser-use (go build 产物)
#   2. ~/.local/bin/obu (symlink)
#   3. Chrome native host 的 stable link (macOS/Linux)
#   4. 本仓库 packages/open-browser-use-cli/native/<平台>/ 二进制
#
# 恢复全局 npm 版本: 删除 ~/.local/bin 下两个文件, 再把 stable link
# 指回 npm 包内的 native 二进制即可。

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
install_dir="${HOME}/.local/bin"
binary_path="${install_dir}/open-browser-use"

# 1. 构建；不要要求用户事先创建安装目录。
mkdir -p "${install_dir}"
(cd "${repo_root}" && go build -o "${binary_path}" ./cmd/open-browser-use)

# macOS Accessibility only belongs to this stable, signed helper identity.
# The CLI invokes it by path; it never asks /usr/bin/osascript or the native
# host to own the permission.
if [[ "$(uname -s)" == "Darwin" ]]; then
  "${repo_root}/scripts/build-mac-approval-helper.sh"
fi

# npm link 的启动器执行仓库包内的二进制，而不是 ~/.local/bin。
# 只更新当前仓库/平台，不覆盖其他全局 npm 包。
goos="$(go env GOOS)"
goarch="$(go env GOARCH)"
package_native="${repo_root}/packages/open-browser-use-cli/native/${goos}-${goarch}"
mkdir -p "${package_native}"
package_binary="${package_native}/open-browser-use"
cp "${binary_path}" "${package_binary}"
cmp "${binary_path}" "${package_binary}"

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
echo "✅ installed binary: ${binary_path}"
echo "✅ updated linked package binary: ${package_binary}"
echo "PATH obu: $(command -v obu || true)"
echo "注意：其他仓库或独立发布包的 PATH 入口不在本脚本更新范围内；需确认实际入口。"
"${binary_path}" version
if "${binary_path}" mcp >/dev/null 2>&1; then
  echo "⚠️  MCP 仍然暴露在 CLI 中"
else
  echo "✅ mcp 未暴露 (预期)"
fi