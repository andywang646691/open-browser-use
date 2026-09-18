# Open Browser Use Installation

Read this reference when the user asks to install, verify, repair, or explain Open Browser Use setup.

## Components

- Chrome extension: the browser-side controller. Installing or enabling it may require the user to approve Chrome prompts.
- Native host and CLI: the local `open-browser-use` binary, also exposed as `obu` when installed from supported packages.

## Install The CLI

Use one of the supported package routes:

```sh
npm install -g open-browser-use
```

```sh
brew install iFurySt/open-browser-use/open-browser-use
```

Verify:

```sh
open-browser-use version
obu version
```

If the short alias is unavailable, use `open-browser-use`.
Running `open-browser-use` with no subcommand prints the CLI version, browser
extension detection status, extension version when available, and the next setup
or upgrade command.

## Set Up A Browser

After installing the CLI, register the native messaging host and open the Chrome Web Store page for the matching extension:

```sh
open-browser-use setup
```

Ask the user to install or enable Open Browser Use from the opened store page. Chrome may ask the user to confirm, enable the extension, or restart. Do not bypass this user step.

For Chrome Beta, register that browser explicitly:

```sh
open-browser-use setup --browser chrome-beta
```

## Self-Hosted Fork / Load Unpacked

When this project is a forked/local build (not tracking the upstream Web Store
item), give the extension its own permanent identity instead of the store
version: the manifest carries a committed `key` (public), so a Developer-mode
`Load unpacked` of `apps/chrome-extension/` always yields the same extension
id, independent of upstream ids and of the checkout directory.

One-time fork setup:

1. Generate a signing key and pin its public half into the manifest (the
   private half stays local, do not commit it):

   ```sh
   mkdir -p keys
   openssl genrsa -out keys/extension-key.pem 2048
   chmod 600 keys/extension-key.pem
   openssl rsa -in keys/extension-key.pem -pubout -outform DER | \
     base64 | tr -d '\n'   # paste into manifest.json as "key"
   ```

   `keys/` is gitignored; the Chrome extension id derives from the public key
   (see `extensionIDFromPublicKey` in `cmd/open-browser-use/main.go`), so it
   stays stable as long as the private key is not lost and the manifest key is
   not changed.

   **This fork's signing key currently lives at `keys/extension-key.pem`**(repo
   root, `chmod 600`, gitignored). Do not commit it, delete it, or overwrite it;
   note that `git clean -x` removes ignored files, so keep a safe backup. Its
   derived extension id is `ldnahhefbkmhjohgiamfnfppaooejmag` —
   `chrome://extensions` should show exactly this. If the key is ever lost:
   regenerate, update the manifest `key`, compute the new id, re-register the
   native host with the new id, and re-load the extension.

2. Load the extension unpacked: `chrome://extensions` → Developer mode →
   `Load unpacked` → `apps/chrome-extension/`. Disable or remove any previous
   store/beta copy so only one native messaging connection exists.

3. Register the native host. The default manifest now allows the built-in
   Web Store, beta-keyed, and this fork's Load unpacked extension IDs, so the
   normal repair command is sufficient:

   ```sh
   open-browser-use install-manifest
   open-browser-use install-manifest --browser chrome-beta
   ```

   For a custom extension build, pass its exact ID explicitly:

   ```sh
   open-browser-use install-manifest --extension-id <custom-extension-id>
   ```

   Restart Chrome (or toggle the extension) so the new native host manifest is
   picked up.

For BitBrowser, install or load the extension in the target BitBrowser instance,
then register the native host manifest into that instance's user-data directory:

```sh
open-browser-use install-manifest --browser <bitbrowser-instance-id>
```

Use `open-browser-use profiles --connected --json` to see BitBrowser instance
ids in the `browserInstance` field once the instance is detectable.

While the Chrome Web Store item is unavailable or pending review, use the release ZIP path:

```sh
open-browser-use setup beta
```

This downloads the latest keyed `open-browser-use-chrome-extension-*.zip` from GitHub Releases and registers the native host for that stable extension id. It opens `chrome://extensions/` and reveals the ZIP in Finder or the system file manager only when the browser extension is missing or older than the CLI-expected version. Ask the user to enable Developer mode and drag that ZIP into the Chrome extensions page when setup prints that next step.

For Chrome Beta, use `open-browser-use setup beta --browser chrome-beta`.

Repair only the native host manifest:

```sh
open-browser-use install-manifest
```

Use `--browser chrome-beta` or `--browser <bitbrowser-instance-id>` to repair a
non-default browser.

Print the manifest without installing:

```sh
open-browser-use manifest
```

## Platform Notes

- macOS and Windows can require the user to approve or enable the extension after Chrome sees it.
- Linux external extension registration can require elevated permissions depending on Chrome installation paths.
- Chrome native messaging host name is `com.ifuryst.open_browser_use.extension`.
- The default socket registry is under `/tmp/open-browser-use/` on Unix-like systems.

## Verification

Run:

```sh
open-browser-use ping --session-id "$OBU_SESSION_ID"
open-browser-use info --session-id "$OBU_SESSION_ID"
open-browser-use user-tabs --session-id "$OBU_SESSION_ID"
```

For one-off installation checks, a temporary session id is enough. Agent browser
tasks should still create and reuse a task-unique session id before opening or
claiming tabs.

If `ping` cannot communicate with Chrome, ask the user whether Chrome is installed and running, whether the extension is enabled, and whether they approved any Chrome prompt. Then use [troubleshooting.md](troubleshooting.md).
