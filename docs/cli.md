# Command-line contract

Kamui uses `github.com/urfave/cli/v3`. Commands return actionable, layer-specific
errors and do not reinterpret an SSH destination as OpenSSH options.

## Primary command

`kamui SSH_DESTINATION [--browser BROWSER] [--browser-loopback MODE]` ensures a session
exists. It does not accept application ports or a `-url` flag. Repeating the
command reuses the same exact-destination session and adds a missing browser
capability when mirroring created the session first. The session keeps its
initially selected browser-loopback mode.

Running `kamui` without arguments prints command help and exits successfully.
`kamui -v` and `kamui --version` print only the build version, such as
`v0.0.1` for a release or `dev` for a development build.

`BROWSER` may be a stable supported browser identifier or an absolute browser
executable path. An ambiguous path requires an explicit browser family.

`MODE` is `remote-only` (the default) or `local-first`. Local-first tries local
IPv4 and IPv6 loopback before the remote host and falls back only after both
local connection attempts are refused.

This option affects only the smart proxy used by Kamui's dedicated browser. It
does not expose ports to ordinary local applications and is unrelated to
`kamui mirror`. `--loopback MODE` remains a deprecated compatibility alias with
the exact `v0.0.4` behavior and prints a concise notice.

## Supporting commands

- `status [SSH_DESTINATION]` reports all sessions or one exact destination.
- `mirror SSH_DESTINATION` enables transparent TCP mirroring without launching
  a browser. It discovers remote listeners and binds the same port on both local
  loopback families. Running it for an existing browser session adds the
  capability in place.
- `stop SSH_DESTINATION` stops one session; `stop --all` stops all sessions.
  With no destination, `stop` lists the known sessions that can be stopped and
  exits unsuccessfully because no stop occurred. It reports when none exist.
- `open SSH_DESTINATION [URL ...]` opens URLs in the matching profile.
- `doctor SSH_DESTINATION` checks local prerequisites and connectivity.
- `browsers` lists detected supported installations.
- `logs` prints the latest 100 lines of OpenSSH background diagnostics.
  `logs --follow` (or `logs -f`) continues streaming appended diagnostics;
  `--lines N` changes the initial line count and `--lines 0` prints all lines.
- `ssh-hook SSH_DESTINATION` requests activation and returns promptly.
- `print-ssh-config SSH_ALIAS` prints, but never installs, a `LocalCommand`
  snippet using `%n`.

Successful `ssh-hook` execution is silent unless verbose output is requested.
The OpenSSH process managed by Kamui always disables `LocalCommand` to prevent
recursion.

Status includes whether mirroring is enabled, mirrored ports, locally occupied
or already-owned conflict ports, and discovery/forwarding errors. Mirror details
appear below each session summary, and port lists wrap at 80 characters so large
listener sets remain readable. Reconciliation runs every five seconds. Existing
local or Kamui listeners win; conflicts are reported and retried. TCP is
supported; UDP is not.

## Exit behavior

Usage errors identify the invalid argument or conflicting flags. Operational
errors identify the failing layer, such as SSH authentication, remote refusal,
browser discovery, proxy availability, or controller communication.

Before any controller-backed command, the CLI authenticates the controller and
compares its protocol/build identity. A stale controller is stopped through the
authenticated channel (or, for `v0.0.4`, by signaling only the kernel-verified
Unix-socket peer), one current controller is started under the existing lock,
and the original command is retried. Takeover is bounded to avoid restart
loops. The requested destination is recreated; other in-memory sessions are
lost and must be invoked again.
