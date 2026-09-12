# ADR 0007: Authenticate controller build identity before commands

- Status: accepted
- Date: 2026-09-12

## Context

Upgrading the installed executable does not replace an already-running
controller. A new CLI can otherwise send fields that an older in-memory wire
implementation silently ignores.

## Decision

Every controller-backed CLI invocation first sends an authenticated read-only
identity probe. Release identity uses link-time version, commit, and build date.
Development identity hashes the executable once at process startup, remaining
stable across invocations of one binary while detecting a rebuild.

On mismatch, a current controller accepts an authenticated shutdown request
that includes its previously observed identity. The `v0.0.4` compatibility path
uses its authenticated status response and the kernel-reported Unix-socket peer
PID; PID files and process names are never trusted. The existing advisory lock
serializes replacement startup. Races re-probe state, bounded waits prevent
restart loops, and the original command is retried automatically.

## Consequences

Normal matching invocations do not restart the controller. Replacement loses
in-memory sessions; the retried command reconstructs its requested destination,
while other destinations must be invoked again. Upgrade handoffs are recorded
as structured `controller_upgrade_restart` diagnostics.
