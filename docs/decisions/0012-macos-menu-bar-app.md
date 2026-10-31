# ADR 0012: Ship a macOS menu bar app as a CLI client

- Status: accepted
- Date: 2026-10-30
- Supersedes in part: ADR 0002

## Context

Users want to see Kamui sessions and mirrored ports, and to connect or stop
sessions, without a terminal. All sessions already live in one user controller
(ADR 0003). That controller's socket protocol is private, and ADR 0007 replaces
the controller whenever a CLI with a different build identity talks to it.
A second copy of the Go code inside an app would therefore restart the
controller back and forth with the user's installed CLI and drop sessions.

The Go release binaries are CGO-free, and Homebrew builds the CLI from source
(ADR 0002). A native menu bar item needs AppKit.

## Decision

The menu bar app is a separate SwiftUI program in `macos/`. It runs the
installed `kamui` executable and never bundles or reimplements Kamui. It talks
to the CLI only through these public contracts:

- `kamui status --json` is the stable status document. Its `format` field
  changes only when a field is removed or changes meaning.
- `kamui mirror --non-interactive DESTINATION` forbids OpenSSH prompts with
  `BatchMode=yes` while still reporting diagnostics and warnings. The explicit
  `mirror` command keeps a destination named like a subcommand a destination.
  The app has no terminal, so a destination that needs a passphrase, host-key
  decision, or other prompt fails promptly. The app then offers to run
  `kamui mirror DESTINATION` in the user's terminal through a generated
  `.command` file, and ADR 0013 relays OpenSSH's prompts there even though the
  app started the controller. The app never handles credentials.
- `kamui stop DESTINATION` and `kamui stop --all`.

`status` and `stop` no longer start a missing controller. When none is
running, status reports no sessions. `status` also reports, rather than
replaces, a controller from a different build. This lets the app poll every
few seconds without leaving a resident controller behind or ending sessions
after an upgrade or when two installations exist.

The app finds `kamui` through an explicit path setting, then the login shell's
`PATH`, then common installation directories, and always shows and lets the
user change its choice. It runs the CLI with the login shell's `PATH` and
`SSH_AUTH_SOCK`, read through a POSIX shell behind a random marker, so a
controller that the app starts sees the same OpenSSH and agent as one started
from a terminal. It captures CLI output in temporary files because a newly
started controller inherits and keeps the invoking command's standard
streams. Timed-out commands receive SIGTERM and then SIGKILL.

The app ships as the `kamui-app` Homebrew cask, which depends on the `kamui`
formula. The release workflow builds a universal app, signs it with a
Developer ID Application certificate under the hardened runtime, notarizes and
staples it, and publishes the archive and cask. A release without the signing
and notarization secrets skips the app. It never publishes an unsigned one.

## Consequences

The Go core remains CGO-free. The app uses the same controller build as the
user's CLI when only one is installed. After a CLI upgrade, the app reports the
stale controller until the user connects again, which replaces it under
ADR 0007. Sessions that the app starts are attributed to Kamui.app for macOS
Local Network privacy, so the app declares `NSLocalNetworkUsageDescription`.
The app requires macOS 13 for `MenuBarExtra` and `SMAppService`. Releases that
include the app need an Apple Developer Program membership and its credentials
in repository secrets. Linux has no menu bar app.
