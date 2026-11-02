# The menu bar app

Kamui's menu bar app runs on macOS 13 or later.

```sh
brew install --cask narasaka/tap/kamui-app
```

You can use Kamui entirely from the menu bar, entirely from a terminal, or
both. The app and the `kamui` command share one background controller, so they
see the same sessions: connect `foo` in the app and `kamui status` lists it;
run `kamui stop foo` and it leaves the menu within a few seconds, and the other
way around. Quitting the app leaves sessions running.

From the menu you can:

- see each session, its mirrored ports, ports held elsewhere, and errors;
- open a mirrored port, such as `http://localhost:3000`, in your browser;
- connect to a `Host` from `~/.ssh/config` or any other destination;
- reconnect a session that is unavailable or needs authentication;
- stop one session or all of them;
- start the app at login.

The icon dims while nothing is connected or a connection is starting, and
shows a dot when a session needs your attention.

## When SSH needs to ask you something

When a host needs a key passphrase, a password, a host-key decision, or a
second factor, the app asks in a window that names the host. Typed answers are
always hidden, including `yes` for a new host key. The app passes each answer
to Kamui in memory and never stores or logs it. Other sessions keep working
while the window is open, and the window closes on its own if the connection
attempt ends, for example when you run `kamui stop foo`. A notice such as
"Confirm user presence for key" appears in the menu next to the connecting
host.

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
usual install locations, then uses its own copy, and shows its choice under
**kamui Executable**, where you can pick another one or **Use Bundled kamui**.
The Homebrew cask also installs the `kamui` formula, so `kamui` works in your
terminal right away.

Two different builds would keep replacing each other's controller and
dropping sessions. The copy inside a release of the app and the formula from
the same release count as one build. When the app notices a controller from
another build, for example right after `brew upgrade`, it says so in the menu
instead of replacing it. Connecting again replaces it, which ends that
controller's other sessions.

If the `kamui` the app finds is older than the app and can't pass prompts to
it, the app says so when you connect. Update that `kamui` or choose **Use
Bundled kamui**.

## Local network access

macOS counts connections from sessions the app started as the app's own. The
first time one reaches a machine on your local network, macOS may ask whether
Kamui can access the local network. If you decline, allow it later in System
Settings > Privacy & Security > Local Network.
