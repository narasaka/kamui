# ADR 0008: Support macOS and Linux clients and destinations

- Status: accepted
- Date: 2026-09-12

## Context

Kamui's proxy and OpenSSH transport are portable, but the initial product
contract assumes a macOS client and Linux `ss` on the SSH destination. Browser
discovery, filesystem paths, release artifacts, and listener discovery must
work when either side is macOS or Linux. Kamui must continue to require no
remote agent.

## Decision

macOS and Linux are supported as both the machine running Kamui and the remote
SSH destination. Linux browser discovery resolves native Chromium- and
Gecko-family executable names through `PATH`; macOS retains application-bundle
discovery. Sandboxed packaging formats are not part of this decision.

OpenSSH is resolved through `PATH` unless explicitly configured. Remote mirror
discovery uses the first available supported socket-inspection tool: `ss`, then
`lsof`, then `netstat`. Its fixed remote shell command emits a tagged format
that the existing discovery seam normalizes to TCP port numbers.

Linux uses XDG configuration, state, and runtime directories. macOS preserves
its Application Support layout. Release builds cover Darwin and Linux on ARM64
and AMD64. `go install github.com/narasaka/kamui/cmd/kamui@latest` is the
platform-neutral source installation path; Homebrew remains supported.

## Consequences

The networking core and remote-agent-free architecture remain unchanged. A
Linux desktop is required to gate native Linux browser behavior before release.
Flatpak, AppImage, BSD, illumos, Windows, UDP, QUIC, and system-wide interception
remain outside the supported contract.
