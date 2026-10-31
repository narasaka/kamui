# ADR 0013: Relay OpenSSH prompts to the requesting command

- Status: accepted
- Date: 2026-10-30
- Amends: ADR 0003

## Context

ADR 0003 runs every OpenSSH child from one background controller and streams
bootstrap diagnostics to the requesting command. OpenSSH reads passphrases,
host-key confirmations, and keyboard-interactive answers from its controlling
terminal, which is the controller's. A controller started by the menu bar app
has no terminal, and one started from another terminal keeps that terminal
even after it closes. In both cases a later `kamui DESTINATION` showed
OpenSSH's prompt text but could not answer it.

## Decision

For a synchronous interactive request, the CLI declares that it can answer
prompts when it has a controlling terminal. The controller then creates a
per-request broker: a user-only Unix socket in the runtime directory with a
random token. It starts the bootstrap OpenSSH child with `SSH_ASKPASS` set to
the `kamui` executable, `SSH_ASKPASS_REQUIRE=force`, and the broker's socket and
token in `KAMUI_ASKPASS_SOCKET` and `KAMUI_ASKPASS_TOKEN`.

When those variables are present, `kamui` acts as the askpass helper. It sends
OpenSSH's prompt to the broker. The broker relays prompts one at a time over the
request's authenticated controller connection. The requesting CLI reads each
answer from its own terminal without echo and sends it back. Prompt text can
contain key paths and server-supplied text, so it never decides whether input
is echoed. The helper prints the answer for OpenSSH. Interrupting a prompt
restores the terminal and abandons the request. Like OpenSSH's
`readpassphrase`, restoring the terminal discards unread input, so part of a
secret typed into a cancelled prompt never reaches the shell. The CLI keeps reading
controller messages while a prompt is open and cancels the prompt when the
attempt ends. The broker closes when the connection attempt finishes, and
reaping OpenSSH waits at most two seconds for output pipes that a helper may
still hold. Answers are never written to logs. If the controller cannot create
the broker socket, it tells the requester and connects without relaying.

The 30-second readiness limit does not count time in which a prompt waits for
an answer, up to five minutes per attempt. The session manager releases its
lifecycle lock while OpenSSH connects and records the attempt, so other
destinations, `stop`, status, and idle expiry proceed while the user types. A
second request for the same destination waits for the attempt. `stop` for that
destination, `stop --all`, idle expiry, and controller shutdown cancel it; a
connection that completes after cancellation is closed rather than published,
and requests waiting behind a stopped attempt fail. Controller shutdown waits
for cancelled attempts, including SSH-hook requests, to stop their OpenSSH
children. Once cancelled, a broker relays no further prompts.

Unattended reconnects, the SSH hook, and `--non-interactive` requests keep
`BatchMode=yes` and receive no broker. Controller protocol version 5 adds the
prompt messages.

## Consequences

Prompts work no matter which process started the controller, including the
menu bar app. Answers pass through the controller's memory and user-only
sockets but never through the menu bar app. Relaying needs OpenSSH 8.4 or later
for `SSH_ASKPASS_REQUIRE`; older clients fall back to the controller's own
terminal, as before. Any `ProxyCommand` that OpenSSH starts during an
interactive bootstrap inherits the broker variables.

Only the tunnel connection relays prompts. Listener discovery still opens its
own `BatchMode=yes` connection, and ADR 0003 keeps Kamui from owning an OpenSSH
control socket that discovery could share. Mirroring therefore needs
authentication that succeeds without a prompt after the first connection, such
as an agent identity. Hosts that prompt on every connection get a working
tunnel and a reported discovery authentication error.
