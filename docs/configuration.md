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
  "ports": {
    "exclude": ["5432"]
  },
  "hosts": {
    "my-dev-server": {
      "browser": "firefox",
      "browserLoopback": "local-first",
      "openBrowserOnSSH": true,
      "idleTimeout": "30m",
      "stopBrowserOnStop": true,
      "ports": {
        "include": ["80", "443"]
      }
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
requests TCP mirroring with the configured port policy. When `openBrowserOnSSH`
is true, it also adds the dedicated browser capability. `idleTimeout` stops a
session after the proxy has no open connections and no recent traffic. Zero
disables idle expiry.
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

## Port policy

Kamui excludes TCP ports 1 through 1023 by default. The optional `ports` object
can add `include` and `exclude` rules globally or for one exact destination.
Each rule is a JSON string containing one port or an inclusive range:

```json
{
  "ports": {
    "exclude": ["5432", "8000-8099"]
  },
  "hosts": {
    "my-dev-server": {
      "ports": {
        "include": ["80", "443"]
      }
    }
  }
}
```

Rules follow the normal precedence order. Kamui starts with the built-in
system-port exclusion, then applies global rules, exact-host rules, and CLI
rules. At each scope it applies `exclude` before `include`, so inclusion wins
when both lists cover the same port. A more specific rule can reverse a less
specific rule.

To include every system port for one host:

```json
{
  "hosts": {
    "my-dev-server": {
      "ports": {
        "include": ["1-1023"]
      }
    }
  }
}
```

Port 0, values above 65535, reversed ranges, and malformed values are rejected.
Configuration changes apply the next time `kamui SSH_DESTINATION`, `kamui mirror
SSH_DESTINATION`, or the SSH hook requests mirroring. Kamui updates an existing
mirror without restarting its SSH transport.
