#!/usr/bin/env bash

set -euo pipefail

if [[ "$(uname -s)" != "Darwin" ]]; then
  echo "macOS approval helper can only be built on macOS" >&2
  exit 1
fi

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
install_dir="${HOME}/Library/Application Support/OpenBrowserUse/approval-helper"
app_dir="${install_dir}/Open Browser Use Approval Helper.app"
executable="open-browser-use-mac-approval-helper"
helper_binary="${app_dir}/Contents/MacOS/${executable}"
version="$(sed -n 's/.*"version": *"\([^"]*\)".*/\1/p' "${repo_root}/packages/open-browser-use-cli/package.json" | head -n 1)"
version="${version:-0.0.0}"

build_dir="$(mktemp -d)"
trap 'rm -rf "${build_dir}"' EXIT
clang -O2 -fobjc-arc -Wall -Wextra -Wno-unused-parameter \
  -framework Cocoa \
  -framework ApplicationServices \
  -o "${build_dir}/${executable}" \
  "${repo_root}/scripts/macos/open-browser-use-mac-approval-helper.m"
"${build_dir}/${executable}" self-test >/dev/null

# Rebuild the bundle from scratch so resources from older helper versions
# (AppleScript fallbacks, loose binaries) never linger.
mkdir -p "${install_dir}"
rm -rf "${app_dir}" "${install_dir}/${executable}"
mkdir -p "${app_dir}/Contents/MacOS"
cp "${build_dir}/${executable}" "${helper_binary}"
chmod 755 "${helper_binary}"
cat > "${app_dir}/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>CFBundleDisplayName</key><string>Open Browser Use Approval Helper</string>
  <key>CFBundleExecutable</key><string>${executable}</string>
  <key>CFBundleIdentifier</key><string>com.ifuryst.open-browser-use.approval-helper</string>
  <key>CFBundleName</key><string>Open Browser Use Approval Helper</string>
  <key>CFBundlePackageType</key><string>APPL</string>
  <key>LSUIElement</key><true/>
  <key>CFBundleShortVersionString</key><string>${version}</string>
  <key>CFBundleVersion</key><string>${version}</string>
</dict></plist>
PLIST

lsregister_path="/System/Library/Frameworks/CoreServices.framework/Frameworks/LaunchServices.framework/Support/lsregister"
if [[ -x "${lsregister_path}" ]]; then
  "${lsregister_path}" -f "${app_dir}" >/dev/null 2>&1 || true
fi

# TCC keys the Accessibility grant on the code signature. An Apple signing
# identity (OBU_MAC_CODESIGN_IDENTITY) keeps the grant across rebuilds; the
# ad-hoc fallback changes on every build, so the helper must be re-enabled in
# Privacy & Security > Accessibility after each rebuild.
codesign_identity="${OBU_MAC_CODESIGN_IDENTITY:--}"
/usr/bin/codesign --force --sign "${codesign_identity}" --timestamp=none \
  --identifier com.ifuryst.open-browser-use.approval-helper "${app_dir}"
/usr/bin/codesign --verify --deep --strict "${app_dir}"
echo "✅ signed macOS approval helper: ${app_dir}"
/usr/bin/codesign -dvv "${app_dir}" 2>&1 | grep -E 'Identifier=|TeamIdentifier=|Authority=|Signature=' || true
if [[ "${codesign_identity}" == "-" ]]; then
  echo "⚠️  ad-hoc signature: re-enable the helper in Accessibility, or run \"${helper_binary}\" setup" >&2
fi
if "${helper_binary}" status >/dev/null 2>&1; then
  echo "helper Accessibility: trusted"
else
  echo "helper Accessibility: not trusted (run \"${helper_binary}\" setup)" >&2
fi
