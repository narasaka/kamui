# Configuration

Kamui reads one optional user-level JSON file:

```text
~/Library/Application Support/kamui/config.json
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
  "openBrowserOnSSH": false,
  "idleTimeout": "0s",
  "stopBrowserOnStop": false,
  "hosts": {
    "my-dev-server": {
      "browser": "firefox",
      "openBrowserOnSSH": true,
      "idleTimeout": "30m",
      "stopBrowserOnStop": true
    }
  }
}
```

Unknown fields are rejected so misspelled security or lifecycle settings do not
silently fall back to defaults. Generated state and browser profiles are stored
under the same application-support directory with user-only permissions.

`openBrowserOnSSH` applies only to the optional SSH hook. `idleTimeout` stops a
session after the proxy has no open connections and no recent traffic; zero
disables idle expiry. `stopBrowserOnStop` defaults to false, leaving the
dedicated browser open with an offline proxy. When true, Kamui also terminates
the browser process it launched for that dedicated profile when the session is
stopped or expires. Existing profile files are preserved in either case.
