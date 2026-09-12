# Install, upgrade, and uninstall

Kamui currently supports macOS on Apple Silicon and Intel.

## Homebrew source installation

Until a tagged formula is published in a tap, install the checked-out formula
from source:

```sh
brew install --HEAD ./Formula/kamui.rb
```

Upgrade it with:

```sh
brew reinstall --HEAD ./Formula/kamui.rb
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
