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

## macOS menu bar app

On macOS 13 or later, install the signed and notarized menu bar app with:

```sh
brew install --cask narasaka/tap/kamui-app
```

The cask depends on the `kamui` formula and installs it when needed. The app
runs that executable. Upgrade or uninstall the app with:

```sh
brew upgrade --cask narasaka/tap/kamui-app
brew uninstall --cask kamui-app
```

Homebrew may ask you to trust the tap first with `brew trust narasaka/tap`.
Uninstalling the app does not stop sessions. Run `kamui stop --all` first if
you want them stopped.

To build the app from source, run `make macos-app` with Xcode installed. It
writes an ad-hoc-signed `dist/Kamui.app`.

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

When none of these secrets exist, the release skips the app with a warning.
When only some exist, the release fails. The workflow never publishes an
unsigned app.

After the release commit is on `main`, push an annotated semantic-version tag:

```sh
git tag -a v0.0.2 -m "Kamui v0.0.2"
git push origin v0.0.2
```

The workflow validates that the tag belongs to `main`, runs all checks, builds
Darwin and Linux binaries for ARM64 and AMD64, publishes them with
`checksums.txt`, and updates the Homebrew formula. With the signing secrets, it
also builds the universal menu bar app, signs it with the hardened runtime,
notarizes and staples it, publishes `Kamui_VERSION_macos.zip`, and writes
`Casks/kamui-app.rb` to the tap from this repository's `Casks/kamui-app.rb`
template. The cask records the archive's SHA-256; `checksums.txt` covers only
the CLI binaries. Do not update the published formula manually
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
