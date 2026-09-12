# Kamui

Kamui is a macOS command-line tool for opening a dedicated development browser
whose explicit loopback URLs connect to the loopback interface of a remote SSH
host. It uses the system OpenSSH client and requires no software on the remote
host.

> [!WARNING]
> Kamui intentionally gives content from the selected remote host the browser
> security treatment associated with `localhost`. Use only hosts you trust and
> keep the Kamui browser profile separate from ordinary browsing.

`kamui mirror` deliberately exposes the selected remote host's TCP services to
every process on the Mac through localhost. Any local application may connect,
and browsers apply localhost-origin security treatment. Mirror only hosts and
services you trust.

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

# Prefer genuine Mac loopback services, falling back to the remote host only
# when neither Mac loopback address accepts the connection.
kamui my-dev-server --browser-loopback local-first

# Make currently listening remote TCP ports available to all Mac applications
kamui mirror my-dev-server
```

The destination is passed to OpenSSH as one positional argument. SSH aliases,
hostnames, and `[user@]host` forms are accepted; values beginning with `-` are
rejected. Ports, jump hosts, and identity files belong in normal OpenSSH
configuration.

Supporting commands are:

```sh
kamui status [SSH_DESTINATION]
kamui mirror SSH_DESTINATION
kamui stop SSH_DESTINATION
kamui stop --all
kamui open SSH_DESTINATION [URL ...]
kamui doctor SSH_DESTINATION
kamui browsers
kamui logs [--follow] [--lines N]
kamui ssh-hook SSH_DESTINATION
kamui print-ssh-config SSH_ALIAS
```

Run `kamui` without arguments to see the command list. `kamui -v` and
`kamui --version` print only the build version. Running `kamui stop` without a
destination lists the known sessions and the commands for stopping one or all
of them; it exits unsuccessfully because no session was stopped.

See [the CLI contract](docs/cli.md) and
[configuration reference](docs/configuration.md) for details.

## Transparent TCP port mirroring

`kamui mirror SSH_DESTINATION` discovers listening TCP ports with `ss` on the
remote host and creates same-numbered listeners on both Mac `127.0.0.1` and
`::1`. Connections from any local TCP client—including ordinary browsers, T3
Code's browser, and `curl http://localhost:PORT`—are carried through Kamui's
existing SSH transport to the same remote loopback port. The command never
launches a browser and requires no port list.

Kamui reconciles discovery every five seconds. A newly discovered listener is
claimed when both Mac loopback addresses are available; a vanished listener is
released. A Mac listener or an already-running Kamui mirror wins a conflict.
The losing destination remains enabled, reports the conflicted port in
`kamui status`, and retries, so it claims the port if the winner later releases
it. This also makes ownership deterministic over time: the existing listener
keeps the port. A local process that starts after Kamui claimed a port initially
receives `address already in use`.

Mirroring uses real unprivileged loopback TCP listeners. It does not change
browser proxy settings, the system HTTP proxy, packet-filter rules, or Network
Extensions. It is TCP-only; UDP services, QUIC, and HTTP/3 are not mirrored.
The remote host must provide `ss`, and discovery failures are shown by
`kamui status` without tearing down already-established listeners.
Each discovery command is bounded to ten seconds and is killed when the mirror
session stops.

Browser and mirror capabilities compose on one exact-destination session.
Running `kamui mirror my-dev-server` adds mirroring to an existing browser
session. Running `kamui my-dev-server` later adds the dedicated browser to a
mirror-created session. `kamui stop my-dev-server` closes the proxy, SSH
transport, and all mirrored listeners.

## Localhost behavior and limitations

Inside a Kamui development profile, `localhost`, names ending in `.localhost`,
IPv4 `127.0.0.0/8`, and IPv6 `::1` refer to the remote loopback interface. The
original port is preserved. Kamui tries remote `127.0.0.1` first and falls back
to remote `::1`, allowing services bound to either address family. In this
default mode, those names cannot simultaneously reach genuine Mac loopback
services from that profile.

This remote-only behavior is the default. With
`--browser-loopback local-first`, Kamui
first tries the requested port on Mac `127.0.0.1` and `::1`. It falls back to
the remote host only when both connections are refused. Other local failures,
HTTP error responses, and TLS errors do not trigger fallback. Existing HTTP
connections and WebSockets remain on the side they originally reached until
they reconnect.

Local-first mode weakens the dedicated profile's isolation: content served by
the remote host may access genuine Mac loopback services and the same browser
origin may be served by different machines as listeners start or stop. Enable
it only for trusted remote hosts and trusted local services.

`--browser-loopback` affects only Kamui's dedicated browser proxy and is
unrelated to `kamui mirror`. The old `--loopback` spelling remains as a
deprecated compatibility alias and prints a notice; its `v0.0.4` behavior is
unchanged.

Without `kamui mirror`, only dedicated-browser TCP traffic is covered. UDP and
HTTP/3 are not transported in either mode, and
Safari is not supported. Ordinary non-loopback browser destinations connect
directly from the Mac. Kamui does not launch, install, or configure remote
application services and does not require Tailscale.

A dropped SSH tunnel breaks existing streams and WebSockets; new connections
resume after a successful bounded reconnect. Browser-native UDP and HTTP/3 do
not cross the proxy. Native applications and terminal commands are unaffected
unless the user explicitly enables `kamui mirror`.
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
- Use `kamui logs` to print the latest OpenSSH background diagnostics, or
  `kamui logs --follow` to stream them. The underlying user-only log is at
  `~/Library/Application Support/kamui/logs/openssh.log` on macOS.
- Use `kamui stop --all` before removing runtime state.
- After an executable upgrade, the first controller-backed command
  authenticates the resident controller, compares protocol/build identity,
  gracefully replaces a stale controller, and retries the command. The
  requested destination is recreated automatically. Other in-memory sessions
  are not reconstructed and must be started again. The
  `controller_upgrade_restart` event is recorded in the controller log.

## Development

The module path is `github.com/narasaka/kamui`. Run the local verification with:

```sh
gofmt -w .
go vet ./...
go test -race ./...
```

Normal commit subjects use the `scope: description` form from
[Scoped Commits](https://scopedcommits.com/).
