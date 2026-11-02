# ADR 0014: Keep reconnecting, and check connections after wakeups

- Status: accepted
- Date: 2026-11-02
- Amends: ADR 0005

## Context

ADR 0005 limited unattended reconnects to five attempts over about three
seconds, then stopped until the user ran Kamui again. Waking a laptop often
takes longer than that to bring back the network: Wi-Fi rejoins, a VPN starts
and its DNS answers, so sessions stayed down after ordinary sleep. A
background reconnect also treated `Could not resolve hostname` as a
configuration error that stopped retries, though that name had already
connected and the error usually meant the network was not up yet.

A connection that died during sleep is noticed only by OpenSSH's server-alive
check, `ServerAliveInterval 15` times `ServerAliveCountMax 3`, so for up to 45
seconds Kamui reported it as connected while new streams hung. A connection
can also survive sleep or a network change, as one over Tailscale often does,
so replacing every connection after a wakeup would break working streams.

## Decision

The session monitor retries a transient failure until it succeeds or the
session stops. It waits 100 milliseconds after the first failure and doubles
the wait after each further one, up to one minute. An authentication or
configuration failure still pauses retries until an explicit command, as
before. Unattended attempts add `ConnectTimeout=10`, so an address that no
longer answers fails quickly and is retried. In unattended attempts only, an
unresolvable hostname is a transient failure; in the foreground it remains a
configuration failure, because there it is usually a typo.

The controller watches for wakeups using only the standard library. Every two
seconds it compares how far the wall clock and the monotonic clock advanced,
because the monotonic clock does not advance while macOS or Linux is
suspended, and a gap of five seconds or more counts as a sleep. It also
compares the machine's routable interface addresses, ignoring loopback and
link-local addresses, which interfaces such as macOS's AirDrop link add and
remove constantly.

After a wakeup, every session probes a connected transport and makes its next
retry immediate. The probe asks the SSH server, through the session's SOCKS
listener, for a stream to remote port 0. Nothing can listen on that port, so a
working server refuses at once and OpenSSH closes the SOCKS stream; a
disposable OpenSSH 9.6 server answered in about 3 milliseconds. Any answer
proves the transport works and keeps it. No answer within five seconds closes
the OpenSSH child, and the monitor reconnects right away.

## Consequences

A session recovers by itself once the network returns, however long that
takes, and within seconds of waking instead of up to 45 seconds later.
Connections that survive a wakeup keep their open streams. A session that can
never reconnect keeps trying about once a minute until it is stopped or
expires. Each probe can make `sshd` log a failed connection to port 0. A false
wakeup, such as a clock adjustment or a new Docker address, costs one probe.

A server that refuses forwarding still answers the probe with a refusal, so
the probe works wherever the SOCKS transport does. A link slower than five
seconds per round trip looks unresponsive after a wakeup, and so does Wi-Fi
that takes several seconds to rejoin; either way the connection is replaced,
though it might have survived. Listener discovery has its
own SSH connection and still recovers through its existing ten-second
silence check. `kamui status --json` keeps its format: a session waiting to
reconnect stays `unavailable` with its last error.
