# Kamui

Kamui is a macOS command-line tool for opening a dedicated development browser
whose explicit loopback URLs connect to the loopback interface of a remote SSH
host. It uses the system OpenSSH client and requires no software on the remote
host.

> [!WARNING]
> Kamui intentionally gives content from the selected remote host the browser
> security treatment associated with `localhost`. Use only hosts you trust and
> keep the Kamui browser profile separate from ordinary browsing.

Kamui is under development and is not yet ready for general installation.

## Command contract

The primary command accepts exactly one OpenSSH destination:

```sh
kamui reyna
kamui narasaka@dev.example.com
kamui reyna --browser firefox
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

## Development

The module path is `github.com/narasaka/kamui`. Run the local verification with:

```sh
gofmt -w .
go vet ./...
go test -race ./...
```

Normal commit subjects use the `scope: description` form from
[Scoped Commits](https://scopedcommits.com/).
