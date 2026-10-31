# Command-line contract

Kamui uses `github.com/urfave/cli/v3`. Commands return errors that identify the
failed layer and do not reinterpret an SSH destination as OpenSSH options.

## Primary command

`kamui SSH_DESTINATION` ensures a session exists and enables transparent TCP
mirroring. It discovers listening ports on the remote host and binds eligible
ports on local IPv4 and IPv6 loopback. It does not launch a browser and does not
accept browser flags or a `-url` flag.

System ports 1 through 1023 are excluded by default. Repeat `--include-port`
or `--exclude-port` to override the policy for one invocation. Each value is a
single TCP port or an inclusive range:

```sh
kamui reyna --include-port 443
kamui reyna --include-port 80 --include-port 443
kamui reyna --include-port 1-1023
kamui reyna --exclude-port 5432
```

These flags select mirrored service ports. They do not select the SSH server's
port, which remains an OpenSSH configuration setting. If both flags cover the
same port, inclusion wins. Invalid ports, reversed ranges, and values outside
1 through 65535 are usage errors.

`kamui mirror SSH_DESTINATION` is a compatibility spelling for the same
operation. Repeating either command reuses the session for that exact
destination. It also replaces the running mirror's port policy and reconciles
listeners immediately without replacing the SSH transport. Command-line rules
last until another mirror command updates the session or the session stops.

In an interactive terminal, the primary command shows a neutral rotating
symbol while it contacts OpenSSH, then clears that line. On success it
prints the same session summary and mirror details as `kamui status
SSH_DESTINATION`. Interactive terminals show connected states in green and
command failures in red; redirected output has no progress message or ANSI
color codes.
The successful primary command ends with the exact `kamui stop SSH_DESTINATION`
command needed to disconnect the session.
OpenSSH passphrase, host-key, and other prompts appear in the terminal that
ran the command, even when the background controller was started elsewhere,
such as by the menu bar app or another terminal. The controller runs OpenSSH
with `SSH_ASKPASS` set to the `kamui` executable and relays each prompt to the
requesting command, which reads the answer from its terminal. Answers,
including `yes` for a new host key, are read without echo and are never
logged. Time spent waiting for an answer does not count toward the 30-second
connection limit, up to five minutes per attempt; a prompt still open when the
attempt ends is cancelled. Unread input is discarded when a prompt ends, so a
partly typed secret never reaches the shell. Other Kamui commands keep working
while a prompt waits, and `kamui stop SSH_DESTINATION` cancels that
destination's connection attempt. When the controller cannot create its prompt
socket, the command prints a notice and connects without relaying.

Prompts are relayed for the tunnel connection only. Listener discovery opens
its own `BatchMode=yes` OpenSSH connection, so mirroring needs authentication
that succeeds without a prompt once the tunnel is up: an ssh-agent identity
(for example with `AddKeysToAgent yes`), a key without a passphrase, or an
OpenSSH `ControlMaster` you configure. Otherwise the session connects but
status reports a discovery authentication error. Relaying requires OpenSSH 8.4 or later on
the local machine.

`--non-interactive` forbids OpenSSH prompts for this connection by adding
`BatchMode=yes`. A destination that needs a passphrase, host-key decision, or
other prompt fails promptly instead of waiting. Diagnostics and warnings, such
as enabled agent forwarding, still reach stderr. Rerun the command without the
flag in a terminal to answer the prompt. The macOS menu bar app uses
`kamui mirror --non-interactive` because it has no terminal; the explicit
`mirror` command keeps a destination named like a subcommand, such as `status`,
a destination. `kamui mirror` accepts the same flags as the primary command.

The controller relays OpenSSH diagnostics from the initial tunnel connection
and listener-discovery command to the calling terminal as they arrive. This
keeps authentication instructions and URLs visible while OpenSSH waits for the
user. Tunnel bootstrap diagnostics also remain in the OpenSSH log and are
included in a failed connection error.

Running `kamui` without arguments prints command help and exits successfully.
`kamui -v` and `kamui --version` print only the build version, such as `v0.0.1`
for a release or `dev` for a development build.

## Dedicated browser command

`kamui browser SSH_DESTINATION [--browser BROWSER] [--browser-loopback MODE]`
starts or reuses an isolated browser profile whose explicit loopback requests
reach the remote host. It adds the browser capability to an existing mirror
session without changing that session's mirroring state.

`BROWSER` may be a supported browser identifier or an absolute executable path.
An ambiguous path requires `--browser-family`.

`MODE` accepts `remote-only`, the default, or `local-first`. Local-first tries
local IPv4 and IPv6 loopback before the remote host. It falls back only after
both local connection attempts receive a refusal.

This option affects only the smart proxy used by the dedicated browser. It does
not expose ports to other local applications. `--loopback MODE` remains a
deprecated compatibility alias with its `v0.0.4` behavior and prints a notice.
The session keeps the loopback mode selected when the browser capability first
starts.

## Supporting commands

- `status [SSH_DESTINATION]` reports all sessions or one exact destination.
  `status --json` prints the machine-readable document described below.
- `mirror SSH_DESTINATION` is the compatibility spelling for the primary
  command and accepts the same port-policy flags.
- `stop SSH_DESTINATION` stops one session, and `stop --all` stops all sessions.
  With no destination, `stop` lists known sessions and exits unsuccessfully
  because it did not stop one.
- `open SSH_DESTINATION [URL ...]` opens URLs in the matching dedicated profile.
- `doctor SSH_DESTINATION` checks local requirements and connectivity.
- `browsers` lists detected supported installations.
- `logs` prints the latest 100 lines of OpenSSH background diagnostics. `logs
  --follow`, or `logs -f`, continues streaming new entries. `--lines N` changes
  the initial count, and `--lines 0` prints the whole log.
- `ssh-hook SSH_DESTINATION` requests mirroring after an OpenSSH login and
  returns after the controller accepts the request. It also requests a browser
  when `openBrowserOnSSH` is true.
- `print-ssh-config SSH_ALIAS` prints a `LocalCommand` snippet that uses `%n`.
  It never installs the snippet.

Successful `ssh-hook` execution is silent unless the caller requests verbose
output. The OpenSSH process managed by Kamui always disables `LocalCommand` to
prevent recursion.

Status includes mirrored ports, excluded ports, ports held by a local process
or another Kamui session, and discovery or forwarding errors. The `EXCLUDED`
line lists only excluded ports currently discovered on the remote host. Mirror
details appear below each session summary. Port lists wrap at 80 characters.
Browser and proxy columns appear only when the displayed results include a
session with the dedicated browser enabled.
Remote listener changes apply within about a second. Existing local or Kamui
listeners keep their ports, and losing sessions retry every five seconds. Kamui supports TCP mirroring but not
UDP.

## Machine-readable status

`kamui status --json [SSH_DESTINATION]` prints one JSON document:

```json
{
  "format": 1,
  "sessions": [
    {
      "destination": "foo",
      "state": "connected",
      "mirror": {
        "enabled": true,
        "mirroredPorts": [3000, 5173],
        "excludedPorts": [22],
        "conflicts": [{ "port": 5432, "error": "address already in use" }]
      }
    }
  ]
}
```

`state` is `connecting`, `connected`, `authentication-required`, or
`unavailable`. Sessions may also carry `lastError` and `browser`, and `mirror`
may carry `lastError`; these fields are omitted when empty. Port lists are
always arrays. Readers should ignore unknown fields. `format` changes only when
a field is removed or changes meaning.

## Exit behavior

Usage errors identify the invalid argument or conflicting flags. Operational
errors identify the failing layer, such as SSH authentication, remote refusal,
browser discovery, proxy availability, or controller communication.

`status` and `stop` never start a controller. When none is running, `status`
reports no sessions, `stop --all` succeeds, and `stop SSH_DESTINATION` reports
that no session exists. `status` and `stop SSH_DESTINATION` also never replace a
running controller from a different build. They fail and say so, so that
polling or stopping one session, such as from the menu bar app, cannot end
every session after an upgrade or when two `kamui` installations exist.
`stop --all` and the other controller-backed commands replace it as described
below.

Before a controller-backed command, the CLI authenticates the controller and
compares its protocol and build identity. If the controller is stale, the CLI
stops it through the authenticated channel. The `v0.0.4` compatibility path
signals only the kernel-verified Unix-socket peer. One current controller then
starts under the existing lock, and the CLI retries the original command. The
takeover has a bounded retry count. Kamui recreates the requested destination,
but the user must start other in-memory sessions again.
