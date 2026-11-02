# ADR 0015: Let the menu bar app work without a terminal or a separate CLI

- Status: accepted
- Date: 2026-11-02
- Supersedes in part: ADR 0012
- Amends: ADR 0013

## Context

ADR 0012 made the menu bar app a client of the separately installed `kamui`
executable and kept it from handling credentials. When OpenSSH needed a
passphrase, a password, a host-key decision, or a second factor, the app could
only offer to rerun the connection in Terminal. Users want to do everything
from the app, everything from a terminal, or mix the two, with both routes
seeing and controlling the same sessions.

All sessions already live in one user controller (ADR 0003), so a session that
either route starts appears in the other's `kamui status`, and either route can
stop it. Two things still tied the app to the terminal: prompts, and the need
to install the CLI separately. ADR 0007 replaces the controller whenever a
`kamui` with a different build identity talks to it, so a second copy of the
CLI must never become a rival build.

## Decision

The app keeps talking to Kamui only through the CLI's public contract. It never
reads the controller socket. The controller protocol stays private, and the
CLI keeps owning the controller handoff of ADR 0007.

### Prompts in the app

`kamui mirror --json-prompts DESTINATION` relays OpenSSH prompts as JSON lines
on standard output and reads answers from standard input. It uses ADR 0013's
broker and controller relay unchanged; only the requesting CLI's prompter
differs. Each prompt is
`{"prompt":{"id":N,"kind":"secret|confirm|notice","text":"…"}}`, a prompt
whose attempt ended is withdrawn with `{"cancel":N}`, and the program replies
with `{"id":N,"answer":"…"}` or `{"id":N,"declined":true}`. Notices need no
reply. A line break ends an answer, as on a terminal. Closed or malformed
input abandons the request. Standard output carries nothing else, so the
mirror summary goes to standard error. A controller that this command starts
receives the null device instead of its standard input and output, so the
command's pipes close when it exits. The flag cannot be combined with
`--non-interactive`.

The app shows each prompt in a floating alert window with a secure text field,
or Allow and Deny buttons for a confirmation, and shows notices in the menu
while the connection waits. Prompt text never decides whether input is shown.
The window is not modal, so polling and other connections continue, and kamui's
cancellation closes it. Time spent waiting for an answer does not count toward
the app's command timeout; the controller's own five-minute limit per attempt
still applies.

This reverses ADR 0012's rule that the app never handles credentials. Answers
now pass through the app's memory and a pipe to its own `kamui` child, then
through the paths ADR 0013 already uses. They are never stored, written to
disk, or logged. Typed text is cleared from the field once the window closes.

The app no longer writes `.command` files or opens Terminal. A session that is
unavailable or needs authentication offers **Reconnect**, which runs the same
in-app connection.

### No separate CLI

The app bundle carries a universal `kamui` in `Contents/Helpers`. The app
executable is `Kamui`, which differs only in case, so the CLI cannot live in
`Contents/MacOS` on a case-insensitive volume. The app chooses, in order: an
explicit path from its settings, `kamui` on the login shell's `PATH`, `kamui`
in the usual installation directories, and its bundled copy. It searches again
on every poll. Whenever the user's terminal has a `kamui` the app can find,
both routes run the same executable, so they cannot replace each other's
controller. With none installed, the app works on its own.

The release builds the bundled CLI with the same link-time version, commit, and
build date as the release binaries and the Homebrew formula. Release build
identity comes from those values (ADR 0007), so the bundled CLI and a formula
of the same release share one identity and one controller even though they are
separate files. Only different releases, or development builds, still hand the
controller over, once, as ADR 0007 describes.

An installed CLI older than the app rejects `--json-prompts`. The app reports
that, names the executable, and offers **Use Bundled kamui** instead of
falling back to a terminal.

### Distribution

`scripts/build-macos-app.sh` builds `kamui` for ARM64 and AMD64 without cgo,
joins them with `lipo`, and signs the CLI with the hardened runtime, a secure
timestamp, and its own identifier before signing the app that seals it, so
notarization covers both. The release workflow passes the release commit and
date to that script. The cask still depends on the `kamui` formula, so
Homebrew users get the CLI on `PATH`, at the same release, without a second
step. CI's menu bar app job installs Go and runs for any Go change, because
the app now contains the CLI.

## Consequences

The app works with no terminal and no separately installed CLI, and a terminal
user never needs the app. Sessions, status, and stop remain shared through the
single controller. The app grows by about 20 MB.

The app now handles secrets in memory. Its prompt window is a target for
spoofing like any password dialog, so it always names the destination it is
connecting to.

Mirroring still needs authentication that succeeds without a prompt after the
first connection (ADR 0013). If the login shell cannot see the terminal's
`kamui`, for example because `PATH` is set only in an interactive startup file
and the CLI lives elsewhere, the app uses its bundled copy and the two routes
can replace each other's controller. The app reports a controller from another
build, and choosing the executable explicitly resolves it.

A controller started by the bundled copy runs from inside `Kamui.app` and
uses that path for its askpass helper. If the app moves or is upgraded while
that controller keeps running, background reconnects cannot reach the helper
until the next app command replaces the controller.

Keeping the cask's formula dependency avoids a conflict for users who already
installed the formula. A later change could have the cask link the bundled CLI
instead and drop the dependency.
