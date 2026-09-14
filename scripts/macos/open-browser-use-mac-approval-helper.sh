#!/usr/bin/env bash
set -eu

case "${1:-}" in
  approve)
    exec /usr/bin/osascript "$(dirname "$0")/../Resources/open-browser-use-approve.applescript"
    ;;
  dismiss-banner)
    exec /usr/bin/osascript "$(dirname "$0")/../Resources/open-browser-use-dismiss-banner.applescript"
    ;;
  *)
    exit 2
    ;;
esac
