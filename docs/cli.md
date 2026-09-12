# Command-line contract

Kamui uses `github.com/urfave/cli/v3`. Commands return actionable, layer-specific
errors and do not reinterpret an SSH destination as OpenSSH options.

## Primary command

`kamui SSH_DESTINATION [--browser BROWSER]` ensures a session exists. It does
not accept application ports or a `-url` flag. Repeating the command reuses the
same exact-destination session.

Running `kamui` without arguments prints command help and exits successfully.
`kamui -v` and `kamui --version` print only the build version, such as
`v0.0.1` for a release or `dev` for a development build.

`BROWSER` may be a stable supported browser identifier or an absolute browser
executable path. An ambiguous path requires an explicit browser family.

## Supporting commands

- `status [SSH_DESTINATION]` reports all sessions or one exact destination.
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

## Exit behavior

Usage errors identify the invalid argument or conflicting flags. Operational
errors identify the failing layer, such as SSH authentication, remote refusal,
browser discovery, proxy availability, or controller communication.
