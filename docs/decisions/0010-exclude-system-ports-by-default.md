# ADR 0010: Exclude system ports by default

- Status: accepted
- Date: 2026-09-14

## Context

Remote hosts commonly expose SSH, web servers, DNS, printing, and other system
services on ports below 1024. Mirroring all of them creates noisy bind conflicts
and exposes services unrelated to the development workflow. A fixed list of
named ports would be arbitrary and would change as services evolve. Ports 80
and 443 can still be useful for development, so the default needs a direct
override.

## Decision

Port discovery continues to report every remote TCP listener. The mirror applies
a port policy after discovery and excludes ports 1 through 1023 by default.
Global configuration, exact-host configuration, and command-line flags can add
exclusions or inclusions in the existing precedence order. At one scope,
inclusions take precedence over exclusions.

`kamui` and `kamui mirror` accept repeatable `--include-port` and
`--exclude-port` flags. Each value is one TCP port or an inclusive range. A new
mirror starts with the effective policy. Repeating either command replaces the
policy on an existing mirror and reconciles it immediately without replacing
the SSH session.

Status distinguishes excluded ports from conflicts. An excluded port caused no
local bind attempt. A conflict means an eligible port could not claim both local
loopback addresses.

## Consequences

The default command no longer exposes remote system ports. Users can opt in one
port, such as 443, or the whole range with `--include-port 1-1023`. Explicitly
included system ports can still become conflicts when the operating system
denies the bind or another local listener owns the port.
