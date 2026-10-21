# Open Browser Use Troubleshooting

Read this reference when setup, connection, browser control, file upload, download, or socket discovery fails.

## First Checks

Start with:

```sh
open-browser-use ping --session-id "$OBU_SESSION_ID"
open-browser-use info --session-id "$OBU_SESSION_ID"
open-browser-use user-tabs --session-id "$OBU_SESSION_ID"
```

For connection checks, set `OBU_SESSION_ID` to a temporary unique value first.
Do not reuse the CLI fallback session for agent browser work.

If these fail:

1. Confirm Chrome is installed.
2. Confirm Chrome is running.
3. Confirm the Open Browser Use extension is installed and enabled.
4. Confirm the native host manifest is installed with `open-browser-use install-manifest` or rerun `open-browser-use setup`.
5. Ask the user to approve any Chrome extension prompt.

Do not silently install, enable, or repair browser integration when the action needs user approval.

## Stale Socket Or Missing Active Host

The CLI first discovers the active socket from the registry. If the registry is
missing, recent CLI versions scan `--socket-dir` for `*.sock` files and connect
to the newest usable socket, then repair the registry. If the registry points to
an unavailable socket, the CLI removes only the stale registry entry and tries
the
same socket-dir scan. It deliberately preserves failed `*.sock` paths: a transient
probe failure must not unlink a still-running native host's listener and make it
impossible to rediscover.

Useful flags:

```sh
open-browser-use ping --socket /tmp/open-browser-use/example.sock
open-browser-use ping --socket-dir /tmp/open-browser-use
open-browser-use ping --timeout 20s
```

If no active host exists, opening Chrome with the extension enabled can allow Chrome to start the native host. Do not start `obu host` manually: the relay is an internal Chrome Native Messaging entry point, and a manually started relay can overwrite `active.json` with a socket that has no Chrome extension peer, causing `read unix ...: i/o timeout`.

## Extension Or Native Host Mismatch

The native host manifest must allow the installed extension id. The default Web Store id is built into the CLI, while `setup beta` uses the keyed GitHub Release ZIP, registers that stable id, and reveals that same ZIP for manual installation.

Use:

```sh
open-browser-use manifest
open-browser-use install-manifest
open-browser-use setup
open-browser-use setup beta
```

If the user installed a custom extension build, pass the extension id explicitly:

```sh
open-browser-use install-manifest --extension-id <chrome-extension-id>
open-browser-use setup --extension-id <chrome-extension-id>
```

## File Upload Issues

Use the Open Browser Use file chooser flow rather than native OS picker automation where possible.

If Chrome blocks local file access for the extension, ask the user to open `chrome://extensions`, open Open Browser Use extension details, and enable file URL access if the task requires local file URLs.

## Permission And Safety Issues

- History, debugger, downloads, tab groups, and broad host access are high-privilege browser capabilities.
- Clipboard reads/writes should happen only for the user-requested task.
- If the user is on a login, payment, approval, CAPTCHA, or destructive workflow, pause and ask before continuing.

## Heredoc Script Crash: `TypeError: 'NoneType' object is not subscriptable`

A script that navigates a fresh tab and then calls
`cdp("Accessibility.getFullAXTree")["nodes"]` could crash with
`TypeError: 'NoneType' object is not subscriptable` even though the
`tabs[0]["id"]`-style setup lines succeeded.

Cause: the extension forwards every CDP event as an id-less `onCDPEvent`
notification (plus periodic `heartbeat` notifications), and the CLI relay
broadcasts them to every connected client. The prelude used to read one
frame per request without correlating ids, so during a heavy page load the
pending events could be misread as the response — `cdp()` then returned
`None`.

The prelude now matches responses by request id and skips notification
frames, so this is fixed in the current CLI. If you still see this crash,
you are running a stale `obu` binary — rebuild/reinstall (e.g.
`make local-install`). Note the no-matching-response symptom can also come
from a genuinely missing CDP result; re-issue the `cdp()` call after the
page settles.

## Event Waits And Response Bodies

Enable the CDP domain before the action and wait in the same script process.
`wait_for_event` consumes only a matching event and retains other events in a
bounded 500-frame tail; it is not a persistent subscription. Filter by the
relevant `event["source"]["tabId"]` and requestId when multiple tabs/requests
are active. Do not call helpers from the predicate or concurrently on threads.

For `Network.getResponseBody`, first wait for `Network.responseReceived`, then
for `Network.loadingFinished` with the same requestId. A response event does
not imply that its body has finished loading. See `help(wait_for_event)` for
the sequence. Network failures, navigation and evicted bodies can still fail;
use the actual observed error rather than assuming every timeout is a bad
predicate.

Request reads use an absolute deadline and preserve incomplete frames after a
read timeout. Waiting or predicate errors restore the prior socket timeout.
A failed send has unknown delivery status and disables the connection: restart
the script and verify state before retrying, especially for actions with side
effects. No browser action is automatically replayed.

Timeout diagnostics strip URL credentials, query and fragment, but paths and
script output may still be private; redact them before sharing logs.

## macOS Remote-Debugging Approval (Direct CDP)

Personal Chrome asks "Allow remote debugging?" / "要允许远程调试吗？" when the
native host opens its Browser Root WebSocket. On macOS the approval helper
presses `Allow` automatically when it has Accessibility permission.

Helper path:
`~/Library/Application Support/OpenBrowserUse/approval-helper/Open Browser Use Approval Helper.app/Contents/MacOS/open-browser-use-mac-approval-helper`

- `status`: prints `trusted=1|0`, never prompts.
- `setup`: interactive guidance (opens Accessibility settings and the helper
  folder). Only run it when the user is present.
- `probe`: reports whether an approval sheet / automation banner is visible
  and whether its button was found, without pressing anything.
- `dump`: prints the browser-native AX tree (web content skipped).

Exit codes: `0` approved, `1` no sheet before the deadline, `2` usage, `3`
Accessibility permission missing, `4` another helper already running, `5`
caller exited while no sheet was visible.

Diagnostics in `~/Library/Logs/OpenBrowserUse/native-host.log`:
`mac_approval_helper_phase_started`, `mac_approval_helper_sheet_visible`,
`mac_approval_helper_pressed` (`control`: `allow` / `banner_close`),
`mac_approval_helper_press_failed` (`axError`),
`mac_approval_helper_finished` (`exitCode`, `sawSheet`).

Common causes:

- `trusted=0` after rebuilding: an ad-hoc signed helper loses its grant on
  every build. Ask the user to re-enable it in Privacy & Security >
  Accessibility (or set `OBU_MAC_CODESIGN_IDENTITY` before building).
- Sheet visible to the user but `probe` reports none: the sheet lives in a
  window the helper cannot enumerate. Chrome attaches it to the most recently
  active browser window, including installed web-app (PWA) windows hosted by
  `com.google.Chrome.app.*` shims, which the helper scans.
- Host stuck in `direct_cdp_connect_started` with no sheet anywhere: an
  orphaned consent sheet from a closed client was answered first. Restart the
  native host (reload the extension) so it opens a fresh Browser Root
  handshake.

## When To Escalate To The User

Ask the user for help when:

- Chrome is not installed.
- Chrome is closed and opening it would interrupt their session.
- Chrome requires extension confirmation or enablement.
- The page requires login, CAPTCHA, hardware key, payment confirmation, or another human-only step.
- The requested browser action affects external systems.
