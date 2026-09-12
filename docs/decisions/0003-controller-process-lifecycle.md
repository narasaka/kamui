# ADR 0003: Supervise sessions in one user controller

- Status: accepted
- Date: 2026-09-11

## Context

The first SSH connection may need host-key or authentication interaction on the
invoking terminal, but the transport must survive that short CLI invocation.
Several CLI processes and optional SSH `LocalCommand` hooks may race to activate
the same destination. OpenSSH multiplexing may or may not be enabled by the
user.

## Decision

The CLI starts one background controller and passes it the invoking standard
streams. The controller starts the system OpenSSH client with those inherited
descriptors and waits for its SOCKS listener before accepting the session as
ready. The CLI waits for that bootstrap result, so initial prompts and
diagnostics remain visible. Once the listener is ready, the long-lived child's
stderr switches to the user-only OpenSSH log instead of retaining the launching
terminal. Later reconnects log from their first byte and add `BatchMode=yes`;
an authentication or configuration failure changes session state to
`authentication-required` and a foreground `kamui DESTINATION` retry is
required.

A user-only advisory lock serializes controller startup. Requests use a
user-only Unix socket plus a random per-controller token. Sessions are keyed by
the hash of the exact destination, so concurrent activation reuses one SSH
transport and proxy. The optional SSH hook requests activation asynchronously
after the controller accepts its authenticated message.

Kamui neither sets nor reads an OpenSSH control socket. If the user's effective
configuration enables `ControlMaster`, OpenSSH may reuse its transport; Kamui's
lifetime and correctness do not depend on that optimization.

A disposable macOS OpenSSH-server test confirms that four concurrent client
sessions can reuse one configured control master without delaying their remote
commands. OpenSSH runs `LocalCommand` when establishing that master, not again
for each multiplexed client, which is sufficient because the first hook creates
the independent Kamui session. The same fixture verifies that a non-multiplexed
`BatchMode=yes` login with an invalid identity fails promptly.

## Consequences

Normal `SIGINT`/`SIGTERM`, explicit stop, idle expiry, and controller shutdown
close the proxy, kill the OpenSSH child, and reap it. A non-catchable controller
death can leave an OpenSSH child until its own server-alive policy detects a
failed connection; no portable macOS parent-death signal exists. Stale socket
state is removed under the startup lock. This is the practical crash-recovery
boundary for version 1; a future signed helper could provide stronger orphan
containment.
