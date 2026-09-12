# Install, upgrade, and uninstall

Kamui currently supports macOS on Apple Silicon and Intel.

## Homebrew installation

Install the current tagged release from the
[`narasaka/homebrew-tap`](https://github.com/narasaka/homebrew-tap) tap. The
fully qualified name lets Homebrew add the tap automatically:

```sh
brew install narasaka/tap/kamui
```

Upgrade it with:

```sh
brew update
brew upgrade narasaka/tap/kamui
```

Uninstall the binary while preserving configuration and development profiles:

```sh
brew uninstall kamui
```

To optionally remove all Kamui configuration, runtime state, and dedicated
browser profiles after uninstalling, move this directory to the Trash:

```text
~/Library/Application Support/kamui
```

## Reproducible release binaries

Maintainers build both architectures and checksums with:

```sh
KAMUI_BUILD_COMMIT=$(git rev-parse HEAD) \
KAMUI_BUILD_DATE=2026-09-11T00:00:00Z \
make package VERSION=1.0.0
```

The Go binaries use `-trimpath`, omit VCS stamping, and clear the Go build ID.
Identical source, Go toolchain, version, commit, and date inputs produce
identical binaries.
