# How Kamui works

## The connection

`kamui foo` hands `foo` to the system OpenSSH client as a single argument and
keeps that connection open in a background controller, so you don't run `ssh`
first. Anything `ssh foo` understands works: aliases, `user@host`, jump hosts,
keys, and Tailscale MagicDNS names. Kamui rejects destinations that start with
`-` so they can't be read as OpenSSH options.

One session exists per exact destination. Running `kamui foo` again reuses it
and applies the current port policy without reconnecting. `kamui stop foo`
closes the session, its OpenSSH process, and every mirrored port.

## Mirroring ports

Kamui keeps one discovery command open over SSH. Every second it lists the
TCP ports listening on the remote machine with `ss`, `lsof`, or `netstat`,
whichever exists. Ports 1 through 1023 are skipped unless you include them
with `--include-port`, and `--exclude-port` skips more.

For each remaining port, Kamui listens on the same port on your laptop's
`127.0.0.1` and `::1` and carries each connection through SSH to the remote
loopback, trying IPv4 first and IPv6 second. A port is mirrored only when both
local addresses are free.

Because discovery runs every second, short-lived ports work too. When a remote
agent starts an OAuth sign-in, such as for an MCP server, its callback port is
mirrored before you finish signing in, so the provider's redirect to
`http://localhost:<port>/callback` reaches the agent instead of your laptop.

When ports collide:

- A program already listening on your laptop keeps its port.
- If two Kamui sessions see the same port, the first one keeps it. The other
  reports a conflict in `kamui status` and retries every five seconds.
- A program that starts on your laptop after Kamui claimed a port gets
  `address already in use`.

If discovery fails or goes quiet for ten seconds, Kamui keeps the current
ports, shows the error in `kamui status`, and restarts discovery five seconds
later. When a remote service stops, its local port goes away on the next pass.

Mirroring only opens unprivileged loopback listeners. It doesn't change proxy
settings, firewall rules, or network extensions, and it doesn't carry UDP,
QUIC, or HTTP/3.

## Reconnecting

A dropped SSH connection breaks open TCP streams and WebSockets. Kamui
reconnects in the background, and new connections work once it succeeds.
Background reconnects never prompt. If one needs a passphrase or another
answer, `kamui status` shows `authentication-required`; run `kamui foo` in a
terminal and Kamui relays OpenSSH's prompts to it.

Port discovery opens its own SSH connection that can't prompt. Mirroring
therefore needs authentication that works without a prompt once you've
connected, such as an ssh-agent key (`AddKeysToAgent yes` helps), a key without
a passphrase, or a `ControlMaster` in your SSH config.

## The dedicated browser

`kamui browser foo` starts an isolated browser profile that sends `localhost`,
`*.localhost`, `127.0.0.0/8`, and `::1` to the remote machine through a local
proxy. Other addresses connect directly. It doesn't open ports for other
programs on your laptop; run `kamui foo` too if you want both. The browser and
mirroring share one session per destination.

With `--browser-loopback local-first`, the profile tries your laptop's
loopback first and falls back to the remote machine only when both local
addresses refuse the connection. HTTP errors, TLS errors, and timeouts don't
trigger the fallback, and open connections stay where they started.

> [!WARNING]
> Remote content in that profile gets the browser's `localhost` trust. In
> local-first mode it can also reach real services on your laptop, and the same
> origin can switch machines when a listener starts or stops. Keep the profile
> for development and use it only with hosts you trust.

Safari isn't supported. On Linux, Kamui finds Chrome, Chromium, Brave, Edge,
Firefox, Firefox ESR, Firefox Developer Edition, Zen, LibreWolf, and Floorp on
your `PATH`, but not Flatpak or AppImage launchers. `kamui browsers` lists what
it found.

## Starting from an SSH login

`kamui print-ssh-config foo` prints a `LocalCommand` snippet that starts
mirroring whenever you `ssh foo`. Review it and add it to your SSH config
yourself. Kamui's own SSH processes always run with `PermitLocalCommand=no`, so
the hook can't call itself. Set `openBrowserOnSSH` to `true` in the
[configuration](configuration.md) to open the dedicated browser as well.

## Upgrades and state

After you upgrade `kamui`, the next command that connects notices the old
controller, replaces it, and retries. That destination comes back on its own;
start any other sessions again. `kamui status` reports the old controller
instead of replacing it, so polling never ends sessions.

OpenSSH diagnostics go to a private log that `kamui logs` prints. On macOS it's
`~/Library/Application Support/kamui/logs/openssh.log`; on Linux it's
`$XDG_STATE_HOME/kamui/logs/openssh.log`, by default
`~/.local/state/kamui/logs/openssh.log`. Run `kamui stop --all` before deleting
Kamui's state.

## Why not something else

Exposing every dev server on a Tailscale network avoids SSH forwards, but it
means changing bind addresses, firewall rules, or project config. A browser
proxy avoids that, but only helps the browser, not `curl` or desktop apps.
Kamui keeps remote services on loopback, needs no list of ports, and works
with any destination plain `ssh` reaches.
