# ADR 0017: Let the menu bar app update itself, and say so only in the app

- Status: accepted
- Date: 2026-11-14
- Amends: ADR 0016

## Context

The menu bar app had no way to learn that a newer release existed. Users who
installed it with Homebrew had to remember to run `brew upgrade`, and nothing
in the app said they were behind. Users want the app to tell them about a new
version and to install it without leaving the app, and they do not want
system notifications for it.

Releases already publish a Developer ID-signed, notarized
`Kamui_X.Y.Z_macos.zip` on GitHub (ADR 0015), and the app's bundle carries the
release version in `CFBundleShortVersionString`.

## Decision

### Finding a release

The app asks GitHub's REST API for the repository's latest release, which
excludes drafts and prereleases, when it starts and every six hours, and when
the user presses **Check now**. A release counts when its tag is
`vMAJOR.MINOR.PATCH`, it is newer than the running app, and it carries the app
archive. A release without the archive, which the release workflow publishes
when signing is not configured, is no update. Development builds, whose
version is `0.0.0`, never check.

The request uses `/usr/bin/curl` through the app's command runner, restricted
to HTTPS, so the app adds no HTTP or TLS dependency and the check is tested
with a fake runner. It sends no identifying data beyond a `Kamui/X.Y.Z`
user agent. Settings has a switch that turns automatic checks off, saved in
`NSUserDefaults` as `automaticUpdateChecks`. A failed automatic check stays
silent; a failed **Check now** says why.

### Telling the user

The app says an update is available only inside itself:

- the status item's menu leads with "Update Available: Kamui X.Y.Z…", which
  opens Settings;
- the window shows a banner under the panel with **Update and restart**,
  **Release notes**, and **Later**, which hides the banner for that version
  until the next launch;
- Settings › Updates shows the state, **Check now**, and the switch.

There are no system notifications and no toasts for updates. The menu bar
icon does not change, because its dot means a session or prompt needs the
user.

### Installing

**Update and restart** stages the update in a hidden folder beside the
running app, so the final move is a rename on one volume:

1. `curl` downloads the release's archive over HTTPS;
2. `ditto` unpacks it;
3. `codesign --verify --deep --strict` checks the unpacked `Kamui.app`
   against a requirement pinned in the app: an Apple-anchored Developer ID
   certificate for team `SWGJKN7CUD` and the identifier
   `io.github.narasaka.kamui`. `--deep` covers the bundled `kamui`;
4. `plutil` confirms the bundle's version is the release's;
5. the running app's bundle is renamed into the staging folder and the new
   bundle renamed into its place. If the second rename fails, the first is
   undone. The staging folder, with the old app, is then deleted.

The running process keeps its open files, so it can still quit cleanly. It
starts a detached `/bin/sh` that waits for the app's process to exit, then
runs `open -n` on the bundle path, and quits. `-n` opens that bundle even if
another copy of Kamui is running. Paths and the process id reach the shell as
arguments, never as script text.

Any failure leaves the installed app untouched, and the banner and Settings
show why with **Try again** and **Download**, which opens the release page.
This covers a folder the user can't write, an app run from somewhere other
than a bundle, and a download that fails the signature check.

Pinning the team in source, rather than trusting whatever signed the running
app, means an ad-hoc-signed development build still accepts only official
releases, and a fork that ships its own app must change the constant.

### Homebrew

The cask declares `auto_updates true`, so a plain `brew upgrade` leaves the
self-updating app alone while `brew upgrade narasaka/tap/kamui-app` or
`--greedy` still upgrades it. An in-app update replaces only the app and its
bundled CLI. The `kamui` formula still updates through Homebrew; when the app
uses a terminal `kamui`, Settings says so next to an available update.

## Consequences

Users see new versions without a terminal and update in one click. Sessions
survive the restart because they live in the controller (ADR 0003). The next
command from the new bundled CLI replaces a controller that the old bundle
started, as ADR 0015 describes for upgrades.

The app trusts GitHub's API for the version number and download link, and
code signing for the bytes. A compromised GitHub account could point at an
older signed build; the version check stops a downgrade only when the API is
honest about the tag, and the plist check requires the bundle to match it.

Homebrew's record of the installed cask version lags after an in-app update
until the next explicit `brew upgrade` of the cask, which then reinstalls the
same release.

Moving the team or bundle identifier requires a release that still satisfies
the old requirement, or users update by hand once.
