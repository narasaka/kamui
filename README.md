# Kamui

Kamui is a macOS command-line tool for opening a dedicated development browser
whose explicit loopback URLs connect to the loopback interface of a remote SSH
host. It uses the system OpenSSH client and requires no software on the remote
host.

> [!WARNING]
> Kamui intentionally gives content from the selected remote host the browser
> security treatment associated with `localhost`. Use only hosts you trust and
> keep the Kamui browser profile separate from ordinary browsing.

Installation, upgrade, uninstall, and release-build instructions are in
[the installation guide](docs/install.md).

## Basic use

Install Kamui, make sure the destination works with the system SSH client, then
run:

```sh
# SSH alias defined in ~/.ssh/config
kamui my-dev-server

# Direct IP address
kamui narasaka@192.168.1.50

# Tailscale MagicDNS machine name (resolved automatically)
kamui narasaka@monitoring

# Full Tailscale MagicDNS name
kamui narasaka@monitoring.yak-bebop.ts.net

# Ordinary DNS hostname
kamui narasaka@dev.example.com
```

Each value after `kamui` is an OpenSSH destination, not a Kamui subcommand.
`my-dev-server` is an example SSH alias. With MagicDNS enabled, Tailscale
automatically resolves a machine name such as `monitoring`, so it needs no
matching entry in `~/.ssh/config`. IP addresses, full MagicDNS names, and
ordinary hostnames also work directly.

Kamui starts its own non-interactive OpenSSH transport, an ephemeral local
proxy, and an isolated development-browser profile. It does not need an
existing terminal session, Tailscale, declared application ports, or software
on the remote host. Start remote services however you normally do, then enter
their unchanged URLs (for example, `http://localhost:3003`) in the dedicated
browser.

Use `kamui status`, `kamui open my-dev-server URL...`, and
`kamui stop my-dev-server` to manage the session. `kamui doctor my-dev-server`
checks the local installation and SSH path.
If the remote service is absent, the proxy reports a remote connection refusal;
Kamui never starts project services itself.

## Command contract

The primary command accepts exactly one OpenSSH destination:

```sh
kamui my-dev-server
kamui narasaka@dev.example.com
kamui my-dev-server --browser firefox
```

The destination is passed to OpenSSH as one positional argument. SSH aliases,
hostnames, and `[user@]host` forms are accepted; values beginning with `-` are
rejected. Ports, jump hosts, and identity files belong in normal OpenSSH
configuration.

Supporting commands are:

```sh
kamui status [SSH_DESTINATION]
kamui stop SSH_DESTINATION
kamui stop --all
kamui open SSH_DESTINATION [URL ...]
kamui doctor SSH_DESTINATION
kamui browsers
kamui ssh-hook SSH_DESTINATION
kamui print-ssh-config SSH_ALIAS
```

See [the CLI contract](docs/cli.md) and
[configuration reference](docs/configuration.md) for details.

## Localhost behavior and limitations

Inside a Kamui development profile, `localhost`, names ending in `.localhost`,
IPv4 `127.0.0.0/8`, and IPv6 `::1` refer to remote `127.0.0.1`. The original
port is preserved. Those names cannot simultaneously reach genuine Mac
loopback services from that profile.

Only browser TCP traffic is covered. UDP and HTTP/3 are not transported, and
Safari is not supported. Ordinary non-loopback browser destinations connect
directly from the Mac. Kamui does not launch, install, or configure remote
application services and does not require Tailscale.

A dropped SSH tunnel breaks existing streams and WebSockets; new connections
resume after a successful bounded reconnect. Browser-native UDP and HTTP/3 do
not cross the proxy. Native applications and terminal commands are unaffected.
Browser compatibility can change between releases. Safari and system-wide
loopback interception are intentionally out of scope.

## Optional SSH login hook

`kamui SSH_DESTINATION` is the normal workflow. To request Kamui activation when
an interactive SSH login succeeds, print (but do not install) a snippet:

```sh
kamui print-ssh-config my-dev-server
```

The snippet uses `%n` to preserve the original alias. The fast, silent hook is
independent of the interactive shell and the Kamui-owned child forces
`PermitLocalCommand=no`, preventing recursion. Review and add the snippet to
your SSH configuration yourself.

## Troubleshooting

- Run `kamui doctor DESTINATION`; it checks `ssh`, connectivity, browser
  discovery, state permissions, port allocation, and the loopback proxy. It
  also warns if effective SSH configuration enables agent forwarding.
- Rerun `kamui DESTINATION` in a terminal if status says authentication is
  required after a network change. Background retries never hide prompts.
- Use `kamui browsers` to see stable browser identifiers and executable paths.
- Use `kamui stop --all` before removing runtime state.

## Development

The module path is `github.com/narasaka/kamui`. Run the local verification with:

```sh
gofmt -w .
go vet ./...
go test -race ./...
```

Normal commit subjects use the `scope: description` form from
[Scoped Commits](https://scopedcommits.com/).
