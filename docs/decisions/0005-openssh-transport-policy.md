# ADR 0005: Use foreground bootstrap and bounded unattended reconnects

- Status: accepted
- Date: 2026-09-11

## Context

Kamui must preserve aliases, jump hosts, keys, agents, host-key checks, and
other user OpenSSH behavior without requiring an existing SSH session. Port
selection races and post-sleep reconnects need predictable failure behavior.

## Decision

Kamui invokes the system OpenSSH executable directly with `-N -T`, a dynamic
forward bound to IPv4 loopback, forwarding readiness checks, server-alive
settings, and mandatory `PermitLocalCommand=no`. All controlled options precede
the exact destination argument. Host-key checking is unchanged.

The initial connection can prompt through the inherited terminal. A bind race
retries with a newly selected ephemeral SOCKS port. Early exit and stderr are
classified as transient, authentication, or configuration failures. After a
connected child exits, Kamui tries at most five unattended reconnects with
exponential delays from 100 ms through 1.6 seconds. Unattended attempts add
`BatchMode=yes`; non-transient failure stops retries until an explicit command.
Effective `ForwardAgent yes` configuration produces a warning but is not
changed.

## Consequences

The remote machine needs only a normal SSH server. Tailscale, OpenSSH
multiplexing, and remote Kamui software are unnecessary. Existing streams fail
when the child exits, while new requests fail quickly until reconnect succeeds.
Remote IPv4, IPv6, and hostname loopback spellings are deliberately normalized
to remote `127.0.0.1` for version 1.
