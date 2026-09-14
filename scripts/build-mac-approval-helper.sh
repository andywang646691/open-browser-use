#!/usr/bin/env bash

set -euo pipefail

if [[ "$(uname -s)" != "Darwin" ]]; then
  echo "macOS approval helper can only be built on macOS" >&2
  exit 1
fi

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
install_dir="${HOME}/Library/Application Support/OpenBrowserUse/approval-helper"
mkdir -p "${install_dir}"

clang -O2 -fobjc-arc \
  -framework Cocoa \
  -framework ApplicationServices \
  -o "${install_dir}/open-browser-use-mac-approval-helper" \
  "${repo_root}/scripts/macos/open-browser-use-mac-approval-helper.m"

# Keep a stable application identity in Accessibility settings. The binary
# itself is enough for launching, while this bundle makes the permission entry
# understandable and stable to the user.
app_dir="${install_dir}/Open Browser Use Approval Helper.app"
helper_binary="${app_dir}/Contents/MacOS/open-browser-use-mac-approval-helper"
mkdir -p "${app_dir}/Contents/MacOS" "${app_dir}/Contents/Resources"
cp "${install_dir}/open-browser-use-mac-approval-helper" "${helper_binary}"
cp "${repo_root}/scripts/macos/open-browser-use-approve.applescript" "${app_dir}/Contents/Resources/open-browser-use-approve.applescript"
cp "${repo_root}/scripts/macos/open-browser-use-dismiss-banner.applescript" "${app_dir}/Contents/Resources/open-browser-use-dismiss-banner.applescript"
cat > "${app_dir}/Contents/Info.plist" <<'PLIST'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>CFBundleDisplayName</key><string>Open Browser Use Approval Helper</string>
  <key>CFBundleExecutable</key><string>open-browser-use-mac-approval-helper</string>
  <key>CFBundleIdentifier</key><string>com.ifuryst.open-browser-use.approval-helper</string>
  <key>CFBundleName</key><string>Open Browser Use Approval Helper</string>
  <key>CFBundlePackageType</key><string>APPL</string>
  <key>LSUIElement</key><true/>
  <key>CFBundleShortVersionString</key><string>0.1.42</string>
  <key>CFBundleVersion</key><string>0.1.42</string>
</dict></plist>
PLIST
chmod 755 "${install_dir}/open-browser-use-mac-approval-helper" "${helper_binary}"

# Re-register the updated bundle with Launch Services before signing.
lsregister_path="/System/Library/Frameworks/CoreServices.framework/Frameworks/LaunchServices.framework/Support/lsregister"
if [[ -x "${lsregister_path}" ]]; then
  "${lsregister_path}" -f "${app_dir}" >/dev/null 2>&1 || true
fi

# Sign the stable app identity. A configured Apple signing identity is best
# because its designated requirement survives rebuilds. Local development
# falls back to an ad-hoc signature so the helper is still code-signed; set
# OBU_MAC_CODESIGN_IDENTITY to a Developer ID / Apple Development identity for
# durable TCC approval across helper rebuilds.
codesign_identity="${OBU_MAC_CODESIGN_IDENTITY:--}"
/usr/bin/codesign --force --sign "${codesign_identity}" --timestamp=none \
  --identifier com.ifuryst.open-browser-use.approval-helper "${helper_binary}"
/usr/bin/codesign --force --sign "${codesign_identity}" --timestamp=none \
  --identifier com.ifuryst.open-browser-use.approval-helper "${app_dir}"
/usr/bin/codesign --verify --deep --strict "${app_dir}"
# Keep the app bundle discoverable from the Accessibility pane.
echo "✅ signed macOS approval helper: ${app_dir}"
/usr/bin/codesign -dvv "${app_dir}" 2>&1 | grep -E 'Identifier=|TeamIdentifier=|Authority=' || true
