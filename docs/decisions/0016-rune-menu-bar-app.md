# ADR 0016: Rebuild the menu bar app in Rust with Rune, with a window and first-run setup

- Status: accepted
- Date: 2026-11-06
- Supersedes in part: ADR 0012, ADR 0015

## Context

The menu bar app (ADR 0012, ADR 0015) was a SwiftUI `MenuBarExtra` whose only
surfaces were a menu and `NSAlert` windows. Everything it could show had to fit
in a menu item: sessions, ports, errors, the executable it chose. Prompts were
alerts, and a first-time user got an icon and nothing else, with no
explanation of what Kamui does or a guided first connection.

[Rune](https://github.com/anomalyco/rune) is a GPU-drawn Rust UI framework
whose apps can render headless, so a view can be checked as a PNG or an
element tree without a display. `rune-kit` and `rune-theme-opencode` supply
its components and a design language. Rune draws only in Rust.

## Decision

The app in `macos/` is a Rust crate. It keeps every contract of ADR 0012 and
ADR 0015: it drives `kamui` through `status --json`, `mirror --json-prompts`
and `stop`, never the controller socket; it finds and bundles `kamui` the
same way; and it handles prompt answers only in memory.

- `core/` holds everything without a window: the process runner, the CLI
  client, the prompt protocol, the status document, the executable locator and
  `~/.ssh/config` hosts. It ports the Swift core and its tests.
- `ui/` holds the window, drawn with Rune and `rune-kit` in OpenCode's tokens,
  following the system appearance. A model owns all state; blocking work goes
  through a `Backend` that reports back with `AppHandle::update`, so the
  window, the menu and the tour are tested headless with a recording backend.
  Named scenes render any state with `--snap`.
- `mac/` is the host: an accessory app with no window at launch, a native
  `NSStatusItem` and `NSMenu` built from `ui::menu`, the login item through
  `SMAppService`, and settings in `NSUserDefaults` under the Swift app's keys.

The status item stays a native menu because a menu opens
instantly and behaves like every other menu bar item. The window adds what a
menu could not hold: a sidebar of sessions, each session's ports with Open,
ports held elsewhere and full errors, a Connect page, and Settings for the
login item and the `kamui` executable.

SSH prompts move from floating alerts to a modal dialog in the window, which
comes forward when a prompt arrives. The dialog always names the destination,
always uses a secure field, and clears the answer when the prompt ends. The
dialog is not system-modal, so polling and other connections continue. Closing
the window leaves the prompt waiting, and the menu leads with "Answer …'s SSH
Prompt…" until it is answered or kamui withdraws it.

A first launch with no sessions opens a short tour over the whole window:
what Kamui does, a checklist of the CLI and hosts it found with the login
item, a real connection to a chosen host with its prompts inline, the ports
arriving, and where Kamui lives afterwards. The tour checks what it can by
itself and holds the "Connected" check for a beat before moving on. Its
completion is saved; a first launch that finds sessions already running, such
as an upgrade from the Swift app, skips it. Settings › Run setup again replays
it.

The app shows a Dock icon only while its window is open, so ⌘Tab reaches the
window, and opening Kamui again from Finder or Spotlight shows the window.

Rune is pre-release. The crate pins one `dev` revision for all of Rune's
crates; `macos/rust-toolchain.toml` pins the Rust toolchain that revision
builds with, and rustup installs it in CI.

## Consequences

The app needs Rust and rustup to build instead of Xcode's Swift toolchain, and
CI's menu bar app job runs `cargo fmt`, `clippy` and `cargo test`. The release
still builds a universal, signed and notarized app; the build script joins
arm64 and x86_64 builds with `lipo`.

The app depends on a pre-release framework fetched from Git. Bumping the pin
means checking the window again; the scenes make that a matter of rendering
them.

The app keeps macOS 13 as its minimum. Rune's own apps target newer releases,
so a change that uses newer AppKit API through Rune would raise it.
