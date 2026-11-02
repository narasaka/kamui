# AGENTS.md

Kamui is a Go CLI for accessing a remote SSH host’s loopback services from macOS. It uses the system OpenSSH client, a local smart proxy, and isolated browser profiles.

## Source of truth

- Treat `PLAN.md` as historical context; the original implementation plan is complete.
- Use `README.md`, `docs/how-it-works.md`, `docs/cli.md`, `docs/configuration.md`, and `docs/menu-bar-app.md` for current behavior.
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

`make check` runs the equivalent repository checks. When changing `macos/`,
`kamui status --json`, or `--non-interactive`, also run `make check-macos-app`.
The menu bar app depends only on the CLI's public contract, never on the
controller socket.

`site/` builds kamui.narasaka.dev from the docs listed in `site/src/lib/docs.ts`.
When changing `docs/` or `site/`, also run `pnpm install && pnpm build` in
`site/`; a broken link between docs fails that build.

Use `scope: description` for commit subjects.
