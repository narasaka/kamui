# ADR 0018: Publish `kamui` as a cask with the app and CLI, and `kamui-cli` as the CLI formula

- Status: accepted
- Date: 2026-11-14
- Supersedes in part: ADR 0002, ADR 0012, ADR 0015, ADR 0017

## Context

The tap published the CLI as the `kamui` formula and the menu bar app as the
`kamui-app` cask, which depended on that formula. The obvious command,
`brew install narasaka/tap/kamui`, installed only the CLI, and a macOS user who
wanted the app needed a second name and `--cask`. The cask didn't pin the
formula's version, so the docs asked users to upgrade both together, and the
formula still compiled the CLI from source even though the app already carried
a signed, notarized copy of it (ADR 0015).

A formula cannot install an app or depend on a cask. When a tap has a formula
and a cask with the same name, Homebrew installs the formula.

## Decision

On macOS, `brew install narasaka/tap/kamui` installs the menu bar app and the
CLI. `kamui` is a cask. Its `binary` stanza links
`Kamui.app/Contents/Helpers/kamui` onto `PATH`, so the terminal and the app
run the same file and upgrade together. It no longer depends on a formula.
The cask keeps `auto_updates true` from ADR 0017; an in-app update replaces
the bundle, so it updates the linked CLI as well. The app treats a `kamui` on
`PATH` that resolves to its bundled copy as that copy, so it doesn't remind
cask users to upgrade a separate CLI, and it warns them that the update
changes the CLI's build. The reminder for a separate CLI names `kamui-cli`.

The CLI-only formula is `kamui-cli`. Linux users and macOS users who don't want
the app install it. It still builds from the tagged source with the release's
version, commit, and build date, so it shares a controller with the app of the
same release. Installing both is unsupported; Homebrew keeps the formula's
link and skips the cask's.

The release writes both from templates in this repository, `Casks/kamui.rb`
and `Formula/kamui-cli.rb`, with `scripts/update-homebrew-tap.rb`. That script
deletes the old `kamui` formula and `kamui-app` cask from the tap and records
`kamui-app` → `kamui` in `cask_renames.json`, so `brew update` moves app
installations to the new cask.

Because `kamui` is now the app, every release must publish a signed app. The
workflow fails when the signing or notarization secrets are missing instead of
skipping the app.

## Considered options

- A `formula_renames.json` entry from `kamui` to `kamui-cli` would make
  `brew install narasaka/tap/kamui` resolve to the formula again, because
  Homebrew follows formula renames before it looks for a cask.
- A `tap_migrations.json` entry from the `kamui` formula to the `kamui` cask
  would migrate formula users automatically, but on Linux it would unlink their
  CLI and then fail to install a macOS-only cask.
- Keeping `kamui` as the formula and making only the cask self-contained keeps
  existing installs untouched but leaves the shortest command installing less
  than macOS users want.

## Consequences

Existing installations keep working but stop receiving upgrades until users
follow the migration steps in `docs/install.md`: remove the old `kamui`
formula, then install `kamui` or `kamui-cli`. `brew update` moves `kamui-app`
to `kamui` on its own, but the old formula keeps the `kamui` link until it is
removed.

`brew install narasaka/tap/kamui` fails on Linux, where the docs point to
`kamui-cli`. Uninstalling the cask also removes `kamui` from `PATH`, so a user
who wants to stop sessions must run `kamui stop --all` first.

The CLI from the cask is a prebuilt, notarized universal binary, so installing
it no longer needs Go.
