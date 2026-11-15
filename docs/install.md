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

On macOS 13 or later, install the signed and notarized menu bar app together
with the `kamui` command:

```sh
brew install narasaka/tap/kamui
```

The cask puts `kamui` on your `PATH` as a link to the copy inside
`Kamui.app`, so the app and your terminal always run the same build. The app
works without the terminal, and the terminal works without opening the app.

The app tells you when a new version is out and updates itself from its
window; see [updates](menu-bar-app.md#updates). Because the terminal's
`kamui` is the app's own copy, that updates both.

On Linux, or to install only the CLI on macOS, Homebrew can build the current
tagged release from source:

```sh
brew install narasaka/tap/kamui-cli
```

Install one or the other. If both are installed, the formula's `kamui` stays
on `PATH` and the cask skips its link.

Upgrade or uninstall with:

```sh
brew update
brew upgrade narasaka/tap/kamui      # or narasaka/tap/kamui-cli
brew uninstall --cask kamui          # or: brew uninstall kamui-cli
```

Homebrew may ask you to trust the tap first with `brew trust narasaka/tap`.
Uninstalling does not stop sessions, and uninstalling the cask removes
`kamui` too, so run `kamui stop --all` first if you want them stopped.

The next controller-backed invocation after an upgrade authenticates the
resident controller, compares its protocol/build identity with the new
executable, gracefully replaces a stale controller, and retries the command.
The requested destination is recreated automatically; other in-memory sessions
must be started again.

### Moving from the old package names

Through v0.0.16, `kamui` was the CLI formula and the app was the `kamui-app`
cask. `brew update` moves a `kamui-app` installation to the `kamui` cask, but
the old formula stays installed and keeps the `kamui` link. Remove it, then
install the package you want:

```sh
brew update
brew uninstall --formula --ignore-dependencies kamui
brew reinstall --cask narasaka/tap/kamui   # you had kamui-app
brew install narasaka/tap/kamui            # you had only the CLI, on macOS
brew install narasaka/tap/kamui-cli        # you had only the CLI, on Linux
```

## Building the menu bar app

To build the app from source, run `make macos-app` with Go and
[rustup](https://rustup.rs) installed; `macos/rust-toolchain.toml` picks the
Rust toolchain. It writes an ad-hoc-signed universal `dist/Kamui.app` with
`kamui` in `Contents/Helpers`. Set `KAMUI_APP_ARCHS=arm64` to build the app for
Apple silicon only.

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

The menu bar app needs a Developer ID Application certificate and an App Store
Connect API key for notarization. Add these repository secrets:

| Secret | Value |
| --- | --- |
| `MACOS_DEVELOPER_ID_P12` | Base64 of the exported Developer ID Application certificate and private key (`base64 -i developer-id.p12`) |
| `MACOS_DEVELOPER_ID_P12_PASSWORD` | Password of that `.p12` export |
| `APPLE_NOTARY_KEY_P8` | Contents of the App Store Connect API key file (`AuthKey_XXXXXXXXXX.p8`) |
| `APPLE_NOTARY_KEY_ID` | That key's ID |
| `APPLE_NOTARY_ISSUER_ID` | The App Store Connect issuer ID |

macOS's `security` tool, which imports the certificate on the release runner,
rejects PKCS#12 files that use OpenSSL 3's default AES and SHA-256 encryption.
The file must also include Apple's Developer ID G2 intermediate certificate, or
the identity is not trusted for code signing. Keychain Access exports a
compatible file. To convert an OpenSSL-created one, run these commands in a
private directory and delete `identity.pem` afterwards:

```sh
curl -fsSLO https://www.apple.com/certificateauthority/DeveloperIDG2CA.cer
openssl x509 -inform DER -in DeveloperIDG2CA.cer -out DeveloperIDG2CA.pem
openssl pkcs12 -in developer-id.p12 -nodes -out identity.pem
openssl pkcs12 -export -in identity.pem -certfile DeveloperIDG2CA.pem \
  -certpbe PBE-SHA1-3DES -keypbe PBE-SHA1-3DES -macalg sha1 \
  -out developer-id-macos.p12
base64 -i developer-id-macos.p12 | tr -d '\n'
```

The release fails before building anything when any of these secrets is
missing, because `brew install narasaka/tap/kamui` installs the app. The
workflow never publishes an unsigned app.

After the release commit is on `main`, push an annotated semantic-version tag:

```sh
git tag -a v0.0.2 -m "Kamui v0.0.2"
git push origin v0.0.2
```

The workflow validates that the tag belongs to `main`, runs all checks, builds
Darwin and Linux binaries for ARM64 and AMD64, publishes them with
`checksums.txt`. It also builds the universal menu bar app with a `kamui` that
has the same release identity as the formula, signs both with the hardened
runtime, notarizes and staples it, and publishes `Kamui_VERSION_macos.zip`.
Finally, `scripts/update-homebrew-tap.rb` writes `kamui-cli.rb` and
`Casks/kamui.rb` to the tap from this repository's `Formula/kamui-cli.rb` and
`Casks/kamui.rb` templates. The cask records the archive's SHA-256;
`checksums.txt` covers only the CLI binaries. Do not update the published
formula or cask manually before the tag exists.

Maintainers can reproduce all release binaries with:

```sh
KAMUI_BUILD_COMMIT=$(git rev-parse HEAD) \
KAMUI_BUILD_DATE=2026-09-11T00:00:00Z \
make package VERSION=0.0.1
```

The Go binaries use `-trimpath`, omit VCS stamping, and clear the Go build ID.
Identical source, Go toolchain, version, commit, and date inputs produce
identical binaries.
