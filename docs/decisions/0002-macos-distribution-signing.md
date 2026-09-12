# ADR 0002: Prefer Homebrew source builds for the initial distribution

- Status: accepted
- Date: 2026-09-11

## Context

Developer ID signing and Apple notarization require externally managed Apple
credentials and release infrastructure. Homebrew can instead compile Kamui from
reviewed source on the user's Mac, while the release process can still produce
deterministic ARM64 and AMD64 binaries with checksums.

## Decision

The initial supported distribution is a Homebrew source build published as the
`kamui` formula in `github.com/narasaka/homebrew-tap`. Users install the tagged
release with `brew install narasaka/tap/kamui`. It does not require a prebuilt
executable to be signed or notarized. The repository also produces unsigned
deterministic binaries for verification and development, but those archives are
not the primary installation path.

Before publishing prebuilt binaries as the default installation path, obtain a
Developer ID Application certificate, sign both architectures, submit the
artifacts for notarization, staple the ticket where applicable, and verify the
downloaded artifact with Gatekeeper.

## Consequences

Initial installations require the Homebrew Go build dependency and take longer
than installing a bottle. Directly downloaded unsigned binaries may be subject
to Gatekeeper or quarantine handling and are not represented as the supported
installation flow.
