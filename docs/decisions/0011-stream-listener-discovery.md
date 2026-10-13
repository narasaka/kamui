# ADR 0011: Stream listener discovery over one OpenSSH command

- Status: accepted
- Date: 2026-10-13

## Context

ADR 0006 polled remote listeners by starting a new non-interactive OpenSSH
process every five seconds. Without a user-configured `ControlMaster`, each
poll performed a full SSH connection and authentication, and a new remote
development server could wait up to five seconds before it was mirrored.
Remote socket inspection itself is cheap. ADR 0003 keeps Kamui from creating
its own OpenSSH control socket.

## Decision

Each mirror keeps one `BatchMode=yes`, `PermitLocalCommand=no` OpenSSH command
open. Its remote POSIX shell loop selects `ss`, `lsof`, or `netstat` once, then
inspects listeners every second. Each pass writes a full snapshot when the
listener set changed, `kamui:same` when it did not, or `kamui:error` with the
tool's exit status. Kamui reconciles only on changed or failed reports.

Every report proves the stream is alive. If no report arrives within the ten
second discovery timeout, or the command exits, Kamui records the failure in
status, keeps the last valid listener set mirrored, and restarts the command
after the five-second reconciliation interval.

Conflict retries no longer depend on discovery. Every five seconds Kamui
reapplies the last streamed snapshot locally, so a port claimed by a local
process or another destination is retried without a remote command. Port
policy changes also reapply that snapshot immediately. Discoverers that cannot
stream are still polled on the reconciliation interval.

## Consequences

A session authenticates once for discovery instead of once per poll, and new
remote listeners usually appear within about a second. The remote host needs no
additional software; the loop uses only POSIX shell features and `sleep 1`.
When OpenSSH exits, the remote shell ends at its next write to the closed
channel. Initial OpenSSH diagnostics still reach the invoking terminal until
the first report. A canceled discovery command may hold its output pipes for
at most two seconds before Kamui stops waiting for it.
