# Configuration

Kamui reads one optional user-level JSON file. Its default path is:

```text
macOS: ~/Library/Application Support/kamui/config.json
Linux: $XDG_CONFIG_HOME/kamui/config.json (default: ~/.config/kamui/config.json)
```

No configuration is required for basic use. Values are resolved in this order,
from highest to lowest precedence:

1. command-line flags
2. the matching exact destination under `hosts`
3. global configuration
4. built-in defaults

Example:

```json
{
  "defaultBrowser": "arc",
  "browserLoopback": "remote-only",
  "openBrowserOnSSH": false,
  "idleTimeout": "0s",
  "stopBrowserOnStop": false,
  "hosts": {
    "my-dev-server": {
      "browser": "firefox",
      "browserLoopback": "local-first",
      "openBrowserOnSSH": true,
      "idleTimeout": "30m",
      "stopBrowserOnStop": true
    }
  }
}
```

Unknown fields are rejected so misspelled security or lifecycle settings do not
silently fall back to defaults. On macOS, generated state and browser profiles
remain under the same application-support directory. On Linux they use
`$XDG_STATE_HOME/kamui` (default: `~/.local/state/kamui`); controller-only
files use `$XDG_RUNTIME_DIR/kamui` when available. Kamui creates its directories
with user-only permissions.

`openBrowserOnSSH` applies only to the optional SSH hook. The hook always
requests the default TCP mirror. When `openBrowserOnSSH` is true, it also adds
the dedicated browser capability. `idleTimeout` stops a session after the proxy
has no open connections and no recent traffic. Zero disables idle expiry.
`stopBrowserOnStop` defaults to false, so the dedicated browser remains open
with an offline proxy. When true, Kamui terminates the browser process it
launched for that profile when the session stops or expires. Kamui preserves
the profile files in either case.

`browserLoopback` accepts `remote-only` or `local-first` and defaults to
`remote-only`. In local-first mode, an available local loopback listener wins;
Kamui uses remote loopback only when both local IPv4 and IPv6 connections are
refused. This permits remote browser content to reach genuine local loopback
services, so use it only with trusted hosts.

This setting affects only `kamui browser` and a browser opened by the SSH hook.
It does not change TCP mirroring. The old `loopback` JSON key remains a
deprecated compatibility alias. Do not set both spellings at the same scope.

`kamui SSH_DESTINATION` and `kamui mirror SSH_DESTINATION` enable port mirroring.
No configuration key disables mirroring for those commands. Mirroring supports
TCP only, binds only `127.0.0.1` and `::1`, and does not support UDP.
