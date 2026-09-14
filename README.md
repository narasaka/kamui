# Kamui

Kamui mirrors the TCP services that listen on loopback on a remote macOS or
Linux machine onto your laptop's loopback interface. If you connect to that
machine with:

```sh
ssh foo
# or
ssh nathan@192.168.1.50
```

run this instead:

```sh
kamui foo
# or
kamui nathan@192.168.1.50
```

The destination is whatever you already pass to `ssh`: an alias from
`~/.ssh/config`, a hostname, or `user@host`. Kamui hands it to the system
OpenSSH client unchanged and manages that connection itself, so you do not run
`ssh` first.

Kamui discovers listening TCP ports on the remote machine and binds eligible
ports on local `127.0.0.1` and `::1`. When a remote service listens on port
3000, `http://localhost:3000` works in your normal browser, in `curl`, and in
any other local TCP client. Kamui installs nothing on the remote machine.

> [!WARNING]
> The primary command exposes the selected remote host's eligible listening TCP
> services to every process on your local machine through loopback. Browsers give
> those services the security treatment associated with `localhost`. Use Kamui
> only with hosts and services you trust.

## Why I made this tool

I moved my development work to an always-on remote machine because I do not
want an agent to stop when I close a laptop, lose Wi-Fi, or install an update.
My laptops are now dumb clients. They stream terminal sessions and agent
responses, while the repository, build processes, and development servers keep
running remotely.

The browser remained local. That split became a problem as soon as an agent
started a development server and printed `http://localhost:3000`. The URL
referred to the remote machine inside the agent's terminal, but it referred to
my laptop when I opened it. The same mismatch affected callback servers,
database consoles, test dashboards, and a frontend that called a backend at a
hard-coded `http://localhost:3003`.

My old setup used Tailscale to reach the machine, SSH to work on it, and one
local port forward for each service:

```sh
ssh \
  -L 3000:127.0.0.1:3000 \
  -L 3001:127.0.0.1:3001 \
  -L 3002:127.0.0.1:3002 \
  -L 3003:127.0.0.1:3003 \
  foo
```

That worked until a repository opened another port. I had to notice the new
port, edit the command or SSH configuration, and reconnect. A monorepo could
use several ports at once, and browser code could refer to a port that I had
not forwarded. Each laptop needed the same setup. The forwards also depended
on the lifetime of the SSH process that created them.

Exposing every development server on the Tailscale network would avoid local
forwards, but it would require changes to bind addresses, firewall rules, or
project configuration. A browser proxy avoids those changes, but it requires a
separate browser profile and does not work for terminal commands or desktop
applications.

Kamui automates the SSH side and discovers ports instead. It keeps remote
services bound to loopback, needs no list of application ports, and does not
change a repository. Tailscale can provide the network path, but Kamui neither
requires nor configures it. Any destination that works with the system SSH
client can work with Kamui.

## Install

Installation, upgrade, uninstall, and release-build instructions are in
[the installation guide](docs/install.md).

Install the latest tagged version with Homebrew on macOS or Linuxbrew:

```sh
brew install narasaka/tap/kamui
```

Or with Go:

```sh
go install github.com/narasaka/kamui/cmd/kamui@latest
```

The local machine needs OpenSSH. The remote machine needs an SSH server with
TCP forwarding plus one of `ss`, `lsof`, or `netstat` for port discovery.

## Basic use

Every example in this README uses `foo` as the SSH destination. Replace it with
the value you pass to `ssh`. Any OpenSSH destination form works:

```sh
kamui foo                       # alias from ~/.ssh/config
kamui nathan@192.168.1.50       # login name and IP address
kamui nathan@foo.example.com    # login name and hostname
kamui monitoring                # Tailscale MagicDNS machine name
```

Kamui accepts only that one positional argument. Keep ports, jump hosts,
identity files, and other connection settings in `~/.ssh/config`. A Tailscale
MagicDNS name such as `monitoring` resolves without an entry there.

Start remote services as you normally do. When a service listens on remote
port 3000, open `http://localhost:3000` in your local browser. You do not need
to declare the port to Kamui or change the service's bind address.

Kamui excludes system ports 1 through 1023 by default. Include one when needed:

```sh
kamui foo --include-port 443
```

Inspect or stop the session with:

```sh
kamui status foo
kamui stop foo
```

`kamui doctor foo` checks the local installation and SSH path. Kamui does not
start project services. If a remote service stops, Kamui releases its local
port after the next discovery pass.

## Dedicated browser mode

The dedicated browser proxy remains available as a narrower alternative:

```sh
kamui browser foo
kamui browser foo --browser firefox
```

This command starts or reuses an isolated browser profile. Inside that profile,
explicit loopback URLs connect to the remote loopback interface through an
ephemeral HTTP proxy. Kamui does not mirror ports for other local applications
unless you also run `kamui foo`.

The default browser mode sends `localhost`, names ending in `.localhost`, IPv4
`127.0.0.0/8`, and IPv6 `::1` to the remote host. It preserves the requested
port, tries remote `127.0.0.1`, and then tries remote `::1`. Those names cannot
reach real local services from that profile while it uses `remote-only` mode.

Use local-first mode when the profile must prefer a service on the laptop:

```sh
kamui browser foo --browser-loopback local-first
```

Local-first mode tries local `127.0.0.1` and `::1` before the remote host. It
falls back only when both local connection attempts receive a refusal. An HTTP
error, a TLS error, or another connection failure does not trigger fallback.
Existing HTTP connections and WebSockets remain connected to their original
side until they reconnect.

Local-first mode lets remote content access real local loopback services. It
can also make the same browser origin refer to a different machine when a
listener starts or stops. Use it only with trusted remote hosts and trusted
local services.

`--browser-loopback` affects only the dedicated browser proxy. The deprecated
`--loopback` spelling has the same behavior and prints a notice. Safari, UDP,
QUIC, and HTTP/3 are not supported by the dedicated browser mode. Non-loopback
browser destinations connect directly from the local machine.

> [!WARNING]
> Kamui gives remote content in the dedicated profile the browser security
> treatment associated with `localhost`. Keep that profile separate from
> ordinary browsing and use only hosts you trust.

## Command contract

The primary command accepts exactly one OpenSSH destination and enables TCP
mirroring:

```sh
kamui SSH_DESTINATION [--include-port PORT_OR_RANGE] [--exclude-port PORT_OR_RANGE]
```

`kamui mirror SSH_DESTINATION` remains as a compatibility spelling for the same
operation. The dedicated browser is a separate command:

```sh
kamui browser SSH_DESTINATION [--browser BROWSER] [--browser-loopback MODE]
```

The destination reaches OpenSSH as one positional argument. Kamui accepts SSH
aliases, hostnames, and `[user@]host` forms. It rejects values beginning with
`-`. Put ports, jump hosts, identity files, and related connection settings in
your normal OpenSSH configuration.

Supporting commands are:

```sh
kamui status [SSH_DESTINATION]
kamui stop SSH_DESTINATION
kamui stop --all
kamui open SSH_DESTINATION [URL ...]
kamui doctor SSH_DESTINATION
kamui browsers
kamui logs [--follow] [--lines N]
kamui ssh-hook SSH_DESTINATION
kamui print-ssh-config SSH_ALIAS
```

`kamui open` opens URLs in an existing dedicated profile. Run `kamui` without
arguments to see command help. `kamui -v` and `kamui --version` print only the
build version. Running `kamui stop` without a destination lists known sessions
and exits unsuccessfully because it did not stop one.

See [the CLI contract](docs/cli.md) and
[configuration reference](docs/configuration.md) for all options.

## How TCP mirroring works

Kamui discovers listening remote TCP ports every five seconds. It excludes
ports 1 through 1023 by default. For each eligible port, it creates same-numbered
listeners on local `127.0.0.1` and `::1`. It carries accepted connections
through its OpenSSH transport to the same port on the remote loopback interface.
It tries remote IPv4 first and remote IPv6 second.

A port becomes active only when Kamui can bind both local loopback addresses.
An existing local listener keeps its port. If two Kamui destinations report the
same port, the first active mirror keeps it. The other session reports a
conflict in `kamui status` and retries every five seconds. It claims the port
after the current owner releases it. A local process that starts after Kamui
has claimed a port receives `address already in use`.

`kamui status` reports excluded ports separately from conflicts. Repeating
`kamui foo` applies the current configuration to the running mirror. Port flags
replace that policy for the active session without restarting its SSH transport.

Discovery uses `ss`, `lsof`, or `netstat` on the remote host. A discovery error
appears in `kamui status` and does not close current listeners. Kamui limits
each discovery command to ten seconds and cancels it when the session stops.

Mirroring uses unprivileged loopback TCP listeners. It does not change browser
proxy settings, the system HTTP proxy, packet-filter rules, or Network
Extensions. It does not mirror UDP, QUIC, or HTTP/3 traffic.

The browser and mirror capabilities share one session for an exact destination.
`kamui browser foo` adds a browser to an existing mirror session.
`kamui foo` adds mirroring to a browser-created session. `kamui stop
foo` closes the proxy, SSH transport, and mirrored listeners.

A dropped SSH connection breaks current TCP streams and WebSockets. New
connections work after a successful bounded reconnect. If a reconnect needs
authentication, rerun `kamui SSH_DESTINATION` in a terminal so OpenSSH can
prompt you.

## Supported platforms

Kamui supports macOS and Linux on the local and remote machines. Release
binaries cover ARM64 and AMD64. Linux browser discovery checks native Chrome,
Chromium, Brave, Edge, Firefox, Firefox ESR, Firefox Developer Edition, Zen,
LibreWolf, and Floorp executable names available through `PATH`. Kamui does not
support Flatpak or AppImage browser launchers.

## Optional SSH login hook

You can ask Kamui to enable mirroring after an interactive SSH login succeeds.
Print the snippet for an SSH alias:

```sh
kamui print-ssh-config foo
```

Review the output and add it to your SSH configuration yourself. The snippet
uses `%n` to retain the original alias. The hook returns after the controller
accepts the request. Kamui sets `PermitLocalCommand=no` on its own SSH process
to prevent recursion. Set `openBrowserOnSSH` to `true` if the hook should also
open the dedicated browser.

## Troubleshooting

- Run `kamui doctor SSH_DESTINATION` to check `ssh`, connectivity, browser
  discovery, state permissions, port allocation, and the loopback proxy. It
  also warns when the effective SSH configuration enables agent forwarding.
- Rerun `kamui SSH_DESTINATION` in a terminal if status reports an authentication
  requirement after a network change. Background retries never hide prompts.
- Run `kamui browsers` to list supported browser identifiers and executable
  paths.
- Run `kamui logs` to print recent OpenSSH diagnostics. Use `kamui logs
  --follow` to stream new entries. The user-only log is at `~/Library/Application
  Support/kamui/logs/openssh.log` on macOS. On Linux it is at
  `$XDG_STATE_HOME/kamui/logs/openssh.log`, which defaults to
  `~/.local/state/kamui/logs/openssh.log`.
- Run `kamui stop --all` before removing runtime state.
- After an executable upgrade, the first controller-backed command checks the
  running controller's protocol and build identity. It replaces a stale
  controller and retries the command. Kamui recreates the requested
  destination. You must start other in-memory sessions again. The controller
  log records a `controller_upgrade_restart` event.

## Development

The module path is `github.com/narasaka/kamui`. Run the repository checks with:

```sh
gofmt -w .
go vet ./...
go test -race ./...
```

Commit subjects use the `scope: description` form from
[Scoped Commits](https://scopedcommits.com/).
