<!-- LOGO -->
<h1>
<p align="center">
  <img src="assets/brand/icon-256.png" alt="Kamui icon" width="128">
  <br>Kamui
</h1>
  <p align="center">
    Use a remote machine's <code>localhost</code> from your laptop.
    <br />
    Mirrors a dev box's ports over the SSH you already use.
    <br />
    <a href="#about">About</a>
    ·
    <a href="#install">Install</a>
    ·
    <a href="#documentation">Documentation</a>
    ·
    <a href="CONTRIBUTING.md">Contributing</a>
    ·
    <a href="#developing">Developing</a>
  </p>
</p>

## About

If you `ssh foo` into a dev box, run `kamui foo` instead. Kamui watches which
TCP ports are listening on the remote machine's loopback and opens the same
ports on your laptop. A dev server on remote port 3000 shows up at
`http://localhost:3000` in your browser, in `curl`, and everywhere else. So do
OAuth redirects back to `localhost`, like the one at the end of an MCP sign-in.
Nothing gets installed on the remote side.

> [!WARNING]
> Every process on your laptop can reach the mirrored services, and browsers
> treat them as `localhost`. Only use Kamui with hosts you trust.

### Why

I do my development on an always-on remote machine, so agents keep working
when I close my laptop or lose Wi-Fi. The laptop just streams terminals.

The browser was the problem. An agent would start a dev server and print
`http://localhost:3000`, which meant the remote machine in its terminal and my
laptop in my browser. I kept a growing list of SSH forwards to paper over it:

```sshconfig
Host foo
	LocalForward 3000 localhost:3000
	LocalForward 3001 localhost:3001
	LocalForward 5173 localhost:5173
	LocalForward 5432 localhost:5432
```

Every new port meant editing that list and reconnecting, on every laptop.

MCP sign-ins broke it completely. An agent on the remote machine prints an
OAuth link, I open it on my laptop, and the provider redirects to
`http://localhost:<port>/callback`, a port the agent opened a moment ago,
usually a random one. The redirect landed on my laptop and the sign-in failed.
No list of forwards can cover a port that changes every time.

Kamui finds the ports for me, including those short-lived callback ports. It
uses the SSH setup I already have, keeps remote services bound to loopback,
and doesn't touch the repository. Tailscale or any other network works, as long
as plain `ssh` does.

## Install

```sh
brew install narasaka/tap/kamui             # CLI
brew install --cask narasaka/tap/kamui-app  # menu bar app + CLI
go install github.com/narasaka/kamui/cmd/kamui@latest
```

Kamui runs on macOS and Linux, on either end. You need OpenSSH locally. The remote machine needs an SSH server with TCP
forwarding and one of `ss`, `lsof`, or `netstat`. See
[installing and upgrading](docs/install.md) for details.

## Usage

```sh
kamui foo                     # mirror foo's ports
kamui status                  # sessions, ports, conflicts
kamui stop foo                # or: kamui stop --all
kamui foo --include-port 443  # ports under 1024 are opt-in
kamui doctor foo              # check your setup
```

The destination can be an alias from `~/.ssh/config`, a hostname, or
`user@host`. Keep ports, jump hosts, and keys in your SSH config; Kamui passes
the destination to OpenSSH as is.

Start your services the way you always do. New ports show up on your laptop
within about a second and go away when the remote service stops.

## Menu bar app

On macOS 13 or later, the menu bar app lists your sessions and their ports,
opens a port in your browser with one click, and connects to hosts from your
SSH config. It drives the `kamui` CLI you installed, so sessions keep running
if you quit it. If a host needs a passphrase or a host-key decision, it opens
your terminal and Kamui asks you there. See [the menu bar app](docs/menu-bar-app.md).

## Dedicated browser

`kamui browser foo` opens an isolated browser profile whose `localhost` points
at the remote machine, without opening any ports on your laptop. See
[the CLI reference](docs/cli.md#dedicated-browser-command) for its modes and
caveats.

## Troubleshooting

- `kamui doctor foo` checks the usual suspects.
- If status says authentication is required, run `kamui foo` in a terminal and
  answer the prompt.
- `kamui logs -f` streams OpenSSH's diagnostics.
- A port that's already taken on your laptop stays with whoever has it.
  `kamui status` lists it as a conflict.

## Documentation

- [How it works](docs/how-it-works.md)
- [CLI reference](docs/cli.md)
- [Configuration](docs/configuration.md)
- [Install, upgrade, and release](docs/install.md)
- [Menu bar app](docs/menu-bar-app.md)
- [Design decisions](docs/decisions/)

## Developing

```sh
make check             # gofmt, golangci-lint, go vet, go test -race
make check-macos-app   # Swift tests for the menu bar app
make macos-app         # build dist/Kamui.app
```

Commit subjects use `scope: description`
([Scoped Commits](https://scopedcommits.com/)).
