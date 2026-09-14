# Install, upgrade, and uninstall

Kamui supports macOS and Linux on ARM64 and AMD64. The machine running Kamui
needs OpenSSH. The dedicated-browser workflow also needs a supported native
browser. An SSH destination needs an SSH server with TCP forwarding. The
default mirroring workflow also needs `ss`, `lsof`, or `netstat` on the
destination.

## Go installation

With the Go toolchain installed, this is the platform-neutral installation
path:

```sh
go install github.com/narasaka/kamui/cmd/kamui@latest
```

The command installs `kamui` into `$GOBIN`, or into `$(go env GOPATH)/bin` when
`GOBIN` is unset. Ensure that directory is on `PATH`, then verify the install:

```sh
kamui --version
```

Run the same `go install` command to upgrade. To uninstall this installation,
remove the `kamui` executable from the Go binary directory.

## Homebrew installation

Homebrew and Linuxbrew can build the current tagged release from source:

```sh
brew install narasaka/tap/kamui
```

Upgrade or uninstall it with:

```sh
brew update
brew upgrade narasaka/tap/kamui
brew uninstall kamui
```

The next controller-backed invocation after an upgrade authenticates the
resident controller, compares its protocol/build identity with the new
executable, gracefully replaces a stale controller, and retries the command.
The requested destination is recreated automatically; other in-memory sessions
must be started again.

## User data

Kamui uses these default paths:

```text
macOS: ~/Library/Application Support/kamui
Linux configuration: $XDG_CONFIG_HOME/kamui (default: ~/.config/kamui)
Linux state and profiles: $XDG_STATE_HOME/kamui (default: ~/.local/state/kamui)
Linux controller runtime: $XDG_RUNTIME_DIR/kamui
```

When `XDG_RUNTIME_DIR` is unavailable, Linux controller files use the
`runtime` directory beneath the Kamui state root. Stop active sessions with
`kamui stop --all` before removing these directories.

## Publishing a release

The tag-driven release workflow requires a `HOMEBREW_TAP_TOKEN` repository
secret with contents write access only to `narasaka/homebrew-tap`.

After the release commit is on `main`, push an annotated semantic-version tag:

```sh
git tag -a v0.0.2 -m "Kamui v0.0.2"
git push origin v0.0.2
```

The workflow validates that the tag belongs to `main`, runs all checks, builds
Darwin and Linux binaries for ARM64 and AMD64, publishes them with checksums,
and updates the Homebrew formula. Do not update the published formula manually
before the tag exists.

Maintainers can reproduce all release binaries with:

```sh
KAMUI_BUILD_COMMIT=$(git rev-parse HEAD) \
KAMUI_BUILD_DATE=2026-09-11T00:00:00Z \
make package VERSION=0.0.1
```

The Go binaries use `-trimpath`, omit VCS stamping, and clear the Go build ID.
Identical source, Go toolchain, version, commit, and date inputs produce
identical binaries.
