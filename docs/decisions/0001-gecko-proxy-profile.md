# ADR 0001: Configure Gecko through an isolated `user.js`

- Status: accepted
- Date: 2026-09-11

## Context

Kamui must proxy HTTP, HTTPS, WebSocket, and secure WebSocket traffic from an
isolated Gecko profile, including destinations Firefox normally exempts as
localhost. It must not modify the installed application or the user's regular
profile.

Mozilla's current [Proxy policy reference](https://firefox-admin-docs.mozilla.org/reference/policies/proxy/)
maps manual HTTP, SSL, and passthrough settings to the `network.proxy.*`
preferences. Firefox's current proxy implementation explicitly bypasses
trustworthy loopback syntax unless
[`network.proxy.allow_hijacking_localhost` is enabled](https://searchfox.org/firefox-main/source/netwerk/base/nsProtocolProxyService.cpp).
Mozilla's source also defines that preference as false by default. The current
[Firefox command-line reference](https://firefox-source-docs.mozilla.org/browser/CommandLineParameters.html)
documents `--profile <path>` and says `--no-remote` implies a separate new
instance.

## Decision

Before every launch, atomically write a user-only `user.js` in Kamui's dedicated
profile with:

- manual proxy mode (`network.proxy.type = 1`);
- the Kamui HTTP proxy as both the HTTP and SSL proxy;
- an empty `network.proxy.no_proxies_on` value;
- `network.proxy.allow_hijacking_localhost = true`.

Launch with `-no-remote -profile PROFILE_PATH`. Opening URLs in an existing
matching profile uses `-profile PROFILE_PATH -new-tab URL` so a second competing
profile process is not deliberately started.

The proxy handles plain WebSocket Upgrade as HTTP and secure WebSocket as a TLS
`CONNECT` tunnel, so no WebSocket-specific Gecko preference is needed.

## Consequences

The user's regular profile and application bundle remain unchanged. The Kamui
profile deliberately loses access to genuine Mac loopback services through the
covered host forms. Fork executable names and profile-lock behavior still need
repeatable tests on each installed fork before release support is claimed.
