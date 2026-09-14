# ADR 0006: Reconcile real dual-stack loopback TCP listeners

- Status: superseded in part by ADR 0008 and ADR 0009
- Date: 2026-09-12

## Context

Native clients need transparent access to currently listening remote
services without per-application proxy settings or declared port lists. Local
services must retain priority, and several SSH destinations may expose the same
port.

## Decision

An opt-in destination mirror runs remote `ss -H -ltn` discovery every five
seconds through non-interactive OpenSSH. Each command has a ten-second timeout
and is canceled during shutdown. Discovery is behind a small interface.
For each desired port Kamui must successfully bind both `127.0.0.1` and `::1`;
if either bind fails it releases the other and records a conflict. Accepted TCP
connections are forwarded through the session's existing OpenSSH SOCKS
transport to the same remote loopback port, trying remote IPv4 then IPv6.

The existing listener wins. A local service therefore wins initial ownership,
and the first active Kamui destination retains a shared port. Losing mirrors
retry on every reconciliation and claim the port after it becomes free.
Removing a remote listener or stopping the session closes listeners and active
connections.

## Consequences

These are ordinary unprivileged loopback listeners: no system proxy, browser
proxy, Network Extension, packet-filter rule, or public bind is involved. UDP,
QUIC, and HTTP/3 are not supported. A local service started after Kamui owns a
port initially receives `address already in use`. The remote host must provide
Linux `ss`; unavailable or malformed output is visible in status and does not
discard the last valid listener set.
