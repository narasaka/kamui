# The menu bar app

Kamui's menu bar app runs on macOS 13 or later.

```sh
brew install --cask narasaka/tap/kamui-app
```

The cask installs the `kamui` CLI too. The app doesn't contain its own copy:
it runs the `kamui` you installed, and the sessions belong to Kamui's
background controller. Quitting the app leaves them running.

From the menu you can:

- see each session, its mirrored ports, ports held elsewhere, and errors;
- open a mirrored port, such as `http://localhost:3000`, in your browser;
- connect to a `Host` from `~/.ssh/config` or any other destination;
- stop one session or all of them;
- start the app at login.

The icon dims while nothing is connected or a connection is starting, and
shows a dot when a session needs your attention.

## When SSH needs to ask you something

The app never answers OpenSSH prompts itself. When a host needs a passphrase, a
host-key decision, or a second factor, the app offers **Connect in Terminal**.
That runs `kamui mirror foo` in your default terminal, and Kamui relays
OpenSSH's prompts there.

Mirroring also needs SSH authentication that works without a prompt after that
first connection, because port discovery uses its own connection. An ssh-agent
key covers most setups; see [reconnecting](how-it-works.md#reconnecting).

## Which kamui it uses

The app looks for `kamui` on your login shell's `PATH`, then in the usual
install locations, and shows its choice under **kamui Executable**, where you
can pick another one.

The app and your terminal need to use the same `kamui`. Two different builds
would keep replacing each other's controller and dropping sessions. When the
app notices a controller from another build, for example right after
`brew upgrade`, it says so in the menu instead of replacing it. Connecting
again replaces it, which ends that controller's other sessions.

## Local network access

macOS counts connections from sessions the app started as the app's own. The
first time one reaches a machine on your local network, macOS may ask whether
Kamui can access the local network. If you decline, allow it later in System
Settings > Privacy & Security > Local Network.
