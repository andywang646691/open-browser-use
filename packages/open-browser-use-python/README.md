# Open Browser Use Python SDK

Python client for controlling a real Chrome profile through Open Browser Use.
The package distribution is `open-browser-use-sdk`; the import module remains
`open_browser_use`.

## Installation

```sh
pip install open-browser-use-sdk
```

The SDK expects the `open-browser-use` CLI and Chrome extension to already be
installed and connected:

```sh
open-browser-use ping
open-browser-use info
```

## Usage

The client also exposes the five Chrome DevTools MCP Extension operations:

```py
installed = browser.client.list_extensions()
installed_id = browser.client.install_extension("/absolute/path/to/ext")["id"]
browser.client.reload_extension(installed_id)
browser.client.trigger_extension_action(installed_id, tab.id)  # omit tab.id to use the session's active tab
browser.client.uninstall_extension(installed_id)
```

These operations use Chrome's browser-root `Extensions.*` CDP domain and
support unpacked extensions only. They require a Chrome instance with unsafe
extension debugging support, normally with `OBU_CDP_MODE=direct` or `auto`.


```py
import json
from pathlib import Path

from open_browser_use import connect_open_browser_use

registry = json.loads(Path("/tmp/open-browser-use/active.json").read_text())

browser = connect_open_browser_use(
    socket_path=registry["socketPath"],
    session_id="python-sdk-example",
)

try:
    browser.client.name_session("Python SDK example - OBU")
    tab = browser.new_tab()
    tab.goto("https://example.com", wait_until="domcontentloaded")
    print(tab.title())
finally:
    browser.client.finalize_tabs([])
    browser.close()
```

Use `OpenBrowserUseClient` directly when you need raw Browser Use JSON-RPC or
CDP methods.
