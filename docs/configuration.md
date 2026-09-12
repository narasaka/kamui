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
  "hosts": {
    "reyna": {
      "browser": "firefox",
      "openBrowserOnSSH": true,
      "idleTimeout": "30m"
    }
  }
}
```

Unknown fields are rejected so misspelled security or lifecycle settings do not
silently fall back to defaults. Generated state and browser profiles are stored
under the same application-support directory with user-only permissions.
