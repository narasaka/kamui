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
Reconciliation runs every five seconds. Existing local or Kamui listeners keep
their ports, and losing sessions retry. Kamui supports TCP mirroring but not
UDP.

## Exit behavior

Usage errors identify the invalid argument or conflicting flags. Operational
errors identify the failing layer, such as SSH authentication, remote refusal,
browser discovery, proxy availability, or controller communication.

Before a controller-backed command, the CLI authenticates the controller and
compares its protocol and build identity. If the controller is stale, the CLI
stops it through the authenticated channel. The `v0.0.4` compatibility path
signals only the kernel-verified Unix-socket peer. One current controller then
starts under the existing lock, and the CLI retries the original command. The
takeover has a bounded retry count. Kamui recreates the requested destination,
but the user must start other in-memory sessions again.
