# The menu bar app

Kamui's menu bar app runs on macOS 13 or later.

```sh
brew install --cask narasaka/tap/kamui-app
```

You can use Kamui entirely from the menu bar, entirely from a terminal, or
both. The app and the `kamui` command share one background controller, so they
see the same sessions: connect `foo` in the app and `kamui status` lists it;
run `kamui stop foo` and it leaves the app within a few seconds, and the other
way around. Quitting the app leaves sessions running.

## First launch

The first time Kamui opens with no sessions running, it walks you through
setup in its window:

1. what Kamui does, and how ports on your server's localhost show up on your
   Mac's;
2. a check of the `kamui` it found, the hosts in `~/.ssh/config`, and whether
   Kamui opens at login;
3. connecting to a host you pick, or any destination you type, answering SSH
   prompts on the page;
4. that host's ports appearing as services start, each with **Open**;
5. where Kamui lives afterwards: the menu bar.

**Skip setup** leaves at any step. Setup runs once; **Run setup again** in
Settings replays it. If sessions are already running when the app first opens,
for example after an upgrade, it skips setup.

## The menu

From the menu bar icon you can:

- see each session, its mirrored ports, ports held elsewhere, and errors;
- open a mirrored port, such as `http://localhost:3000`, in your browser;
- connect to a `Host` from `~/.ssh/config`, or choose **Other Destination…**;
- reconnect a session that is unavailable or needs authentication;
- stop one session or all of them;
- open the Kamui window or its settings, and choose whether Kamui opens at
  login.

The icon dims while nothing is connected or a connection is starting, and
shows a dot when a session or an SSH prompt needs your attention.

## The window

**Open Kamui…** in the menu, or opening Kamui again from Finder or Spotlight,
shows the window. It lists sessions in a sidebar. Each session's page shows its
state, its mirrored ports with **Open**, which opens them in your default
browser, ports held elsewhere on your Mac, ports it doesn't mirror, and its full
errors, with **Reconnect** and **Stop**. For a browser whose `localhost` is the
host's, run `kamui browser` (see the [CLI reference](cli.md)). **Connect** (⌘N) connects to any destination or a
host from `~/.ssh/config`, and **Settings** (⌘,) holds the login item, the
`kamui` executable, and **Run setup again**.

While the window is open, Kamui has a Dock icon so ⌘Tab reaches it. Closing the
window (⌘W) removes the icon; Kamui stays in the menu bar.

## When SSH needs to ask you something

When a host needs a key passphrase, a password, a host-key decision, or a
second factor, the Kamui window comes forward and asks in a dialog that names
the host. Typed answers are always hidden, including `yes` for a new host key.
The app passes each answer to Kamui in memory and never stores or logs it.
Other sessions keep working while the dialog is open, and it closes on its own
if the connection attempt ends, for example when you run `kamui stop foo`.
**Cancel** stops connecting to that host. If you close the window instead, the
question waits, and the menu offers to answer it. A notice such as "Confirm
user presence for key" appears in the menu and on the session's page while the
host connects; you can stop it from either.

A Tailscale SSH check opens its "To authenticate, visit" link in your browser
while the connection waits. The link must use HTTPS. Because the SSH server
chooses the link, the app asks before opening one that isn't on
`login.tailscale.com`, such as a self-hosted Headscale server's. Kamui waits up
to 30 seconds for you to sign in.

Mirroring also needs SSH authentication that works without a prompt after that
first connection, because port discovery uses its own connection. An ssh-agent
key covers most setups; see [reconnecting](how-it-works.md#reconnecting).

## Which kamui it uses

The app contains its own copy of `kamui`, so it works without installing
anything else. When your terminal has a `kamui`, the app uses that one instead,
so both use the same build. It looks on your login shell's `PATH`, then in the
usual install locations, then uses its own copy, and shows its choice in
Settings, where you can **Choose…** another one, **Use bundled kamui**, or go
back to **Find automatically**. The Homebrew cask also installs the `kamui`
formula, so `kamui` works in your terminal right away.

Two different builds would keep replacing each other's controller and
dropping sessions. The copy inside a release of the app and the formula from
the same release count as one build. When the app notices a controller from
another build, for example right after `brew upgrade`, it says so in the menu
and the window instead of replacing it. Connecting again replaces it, which
ends that controller's other sessions.

If the `kamui` the app finds is older than the app and can't pass prompts to
it, the app says so when you connect. Update that `kamui` or choose **Use
bundled kamui**.

## Local network access

macOS counts connections from sessions the app started as the app's own. The
first time one reaches a machine on your local network, macOS may ask whether
Kamui can access the local network. If you decline, allow it later in System
Settings > Privacy & Security > Local Network.
