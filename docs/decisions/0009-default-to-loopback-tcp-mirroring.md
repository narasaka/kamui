# ADR 0009: Default to loopback TCP mirroring

- Status: accepted
- Date: 2026-09-13

## Context

The dedicated browser keeps remote loopback routing inside an isolated profile,
but it asks users to leave their normal browser and does not help other local
TCP clients. Loopback TCP mirroring works with the user's existing browser,
terminal tools, and desktop applications without changes to a project.

## Decision

`kamui SSH_DESTINATION` enables loopback TCP mirroring and does not launch a
browser. `kamui mirror SSH_DESTINATION` remains as a compatibility spelling.
`kamui browser SSH_DESTINATION` adds the dedicated browser capability and owns
the browser selection and loopback flags. The OpenSSH login hook enables
mirroring and may also add the browser when `openBrowserOnSSH` is true.

Both capabilities continue to share the session for one exact SSH destination.

## Consequences

The common command now exposes discovered remote TCP listeners to every local
process through `127.0.0.1` and `::1`. Existing local listeners keep their
ports, conflicts remain visible in status, and the security warning belongs in
the primary documentation. Users who prefer the narrower browser proxy must
request it with the `browser` command.
