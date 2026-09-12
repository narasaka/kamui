# ADR 0004: Reopen the exact dedicated browser profile

- Status: accepted
- Date: 2026-09-11

## Context

Chromium may ignore new proxy arguments when an invocation is handed to an
unrelated running browser. Gecko profiles are locked while in use. Kamui must
never modify or accidentally reuse a normal browser profile.

## Decision

Every browser and exact SSH destination gets a user-only profile directory.
Every Chromium invocation includes that directory, the current Kamui proxy,
`<-loopback>`, and `--no-first-run`. Reopening repeats the exact profile key;
Chromium routes the URL to the matching profile process instead of an unrelated
default-profile process. The initially ephemeral loopback proxy address is
persisted per exact destination and rebound after controller restart, so a
browser intentionally left open never retains a stale endpoint. If that saved
port is unexpectedly occupied, startup fails instead of silently moving the
proxy behind the browser's back. Gecko starts with `-no-remote -profile PATH`;
when its `.parentlock` shows that profile is already running, later opens target
the same profile with `-profile PATH -new-tab URL`.

Kamui retains the process handle for a browser it starts. The default stop
policy leaves the dedicated browser open. With `stopBrowserOnStop` enabled,
Kamui terminates only the retained process keyed by the exact executable and
profile path. Profile contents remain available for later reuse.

Tor Browser is rejected because redirecting its traffic would violate its
routing and privacy model. Gecko fork adapters are discoverable only at their
known application paths; installed forks require their repeatable macOS gate
before release support is claimed.

## Consequences

An already-running matching profile is the browser's single-instance boundary;
Kamui does not use global browser automation. A browser that fully daemonizes
away from the launched process may remain open even under the opt-in close
policy, while the proxy and SSH transport are still stopped safely.
