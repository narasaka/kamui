# AGENTS.md

Kamui is a Go CLI for accessing a remote SSH host’s loopback services from macOS. It uses the system OpenSSH client, a local smart proxy, and isolated browser profiles.

## Source of truth

- Treat `PLAN.md` as historical context; the original implementation plan is complete.
- Use `README.md`, `docs/cli.md`, and `docs/configuration.md` for current behavior.
- Consult `docs/decisions/` before changing established architecture or lifecycle behavior.

## Development

- Keep the networking core testable without real browsers or SSH hosts.
- Prefer the Go standard library and small interfaces for external processes, clocks, dialers, and filesystem access.
- Preserve existing security boundaries: loopback-only listeners, isolated browser profiles, normal SSH host-key verification, and redacted logs.
- Pass SSH destinations as one positional argument, reject values beginning with `-`, and keep `PermitLocalCommand=no` on Kamui-managed SSH processes.
- Add or update tests with every behavioral change.

Run before handing off changes:

```sh
gofmt -w .
go vet ./...
go test -race ./...
```

`make check` runs the equivalent repository checks.

Use `scope: description` for commit subjects.
