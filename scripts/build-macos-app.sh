#!/usr/bin/env bash
# Builds the universal Kamui menu bar app and its release archive.
#
# The app carries a universal kamui CLI in Contents/Helpers. It uses the same
# link-time version, KAMUI_BUILD_COMMIT, and KAMUI_BUILD_DATE as the release's
# CLI binaries and Homebrew formula, so all of them share one controller build
# identity.
#
# The app itself is Rust (macos/, drawn with Rune). It is built for arm64 and
# x86_64 and joined with lipo; KAMUI_APP_ARCHS (for example "arm64") builds
# fewer, for a toolchain without the other target.
#
# Signing uses KAMUI_CODESIGN_IDENTITY, or an ad-hoc signature when unset.
# Notarization runs when KAMUI_NOTARY_KEY (an App Store Connect API key file),
# KAMUI_NOTARY_KEY_ID, and KAMUI_NOTARY_ISSUER are all set; it requires a
# Developer ID identity.
set -euo pipefail

if [[ $# -ne 1 || ! "$1" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "usage: scripts/build-macos-app.sh VERSION (MAJOR.MINOR.PATCH)" >&2
  exit 2
fi

version="$1"
repository_root="$(cd "$(dirname "$0")/.." && pwd)"
output_root="${KAMUI_OUTPUT_ROOT:-dist}"
mkdir -p "$output_root"
output_root="$(cd "$output_root" && pwd)"
identity="${KAMUI_CODESIGN_IDENTITY:--}"
notarize=false
if [[ -n "${KAMUI_NOTARY_KEY:-}" || -n "${KAMUI_NOTARY_KEY_ID:-}" || -n "${KAMUI_NOTARY_ISSUER:-}" ]]; then
  if [[ -z "${KAMUI_NOTARY_KEY:-}" || -z "${KAMUI_NOTARY_KEY_ID:-}" || -z "${KAMUI_NOTARY_ISSUER:-}" ]]; then
    echo "set all of KAMUI_NOTARY_KEY, KAMUI_NOTARY_KEY_ID, and KAMUI_NOTARY_ISSUER to notarize" >&2
    exit 2
  fi
  if [[ "$identity" == "-" ]]; then
    echo "notarization requires KAMUI_CODESIGN_IDENTITY to name a Developer ID Application identity" >&2
    exit 2
  fi
  notarize=true
fi

commit="${KAMUI_BUILD_COMMIT:-$(git -C "$repository_root" rev-parse HEAD)}"
build_date="${KAMUI_BUILD_DATE:-unknown}"
ldflags="-s -w -buildid= -X github.com/narasaka/kamui/internal/version.Version=${version} -X github.com/narasaka/kamui/internal/version.Commit=${commit} -X github.com/narasaka/kamui/internal/version.BuildDate=${build_date}"
helper_build="$(mktemp -d)"
trap 'rm -rf "$helper_build"' EXIT
for architecture in arm64 amd64; do
  (
    cd "$repository_root"
    CGO_ENABLED=0 GOOS=darwin GOARCH="$architecture" \
      go build -trimpath -buildvcs=false -ldflags "$ldflags" -o "${helper_build}/kamui-${architecture}" ./cmd/kamui
  )
done

package_root="${repository_root}/macos"
binaries=()
for architecture in ${KAMUI_APP_ARCHS:-arm64 x86_64}; do
  case "$architecture" in
    arm64) target=aarch64-apple-darwin ;;
    x86_64) target=x86_64-apple-darwin ;;
    *)
      echo "unknown architecture in KAMUI_APP_ARCHS: $architecture" >&2
      exit 2
      ;;
  esac
  # From inside macos/, so rustup follows its rust-toolchain.toml.
  (
    cd "$package_root"
    MACOSX_DEPLOYMENT_TARGET=13.0 cargo build --locked --release --target "$target" --bin kamui-app
  )
  binaries+=("${package_root}/target/${target}/release/kamui-app")
done

app="${output_root}/Kamui.app"
rm -rf "$app"
mkdir -p "${app}/Contents/MacOS" "${app}/Contents/Helpers" "${app}/Contents/Resources"
lipo -create -output "${app}/Contents/MacOS/Kamui" "${binaries[@]}"
lipo -create -output "${app}/Contents/Helpers/kamui" "${helper_build}/kamui-arm64" "${helper_build}/kamui-amd64"
cp "${package_root}/Resources/AppIcon.icns" "${app}/Contents/Resources/AppIcon.icns"
sed "s/KAMUI_VERSION/${version}/g" "${package_root}/Resources/Info.plist" > "${app}/Contents/Info.plist"
plutil -lint "${app}/Contents/Info.plist" >/dev/null

# Sign the nested CLI before the app that seals it.
helper="${app}/Contents/Helpers/kamui"
if [[ "$identity" == "-" ]]; then
  codesign --force --identifier io.github.narasaka.kamui.cli --sign - "$helper"
  codesign --force --sign - "$app"
else
  codesign --force --options runtime --timestamp --identifier io.github.narasaka.kamui.cli --sign "$identity" "$helper"
  codesign --force --options runtime --timestamp --sign "$identity" "$app"
fi
codesign --verify --strict --deep --verbose=2 "$app"
"$helper" --version >/dev/null

archive="${output_root}/Kamui_${version}_macos.zip"
rm -f "$archive"
ditto -c -k --sequesterRsrc --keepParent "$app" "$archive"

if [[ "$notarize" == true ]]; then
  xcrun notarytool submit "$archive" \
    --key "$KAMUI_NOTARY_KEY" --key-id "$KAMUI_NOTARY_KEY_ID" --issuer "$KAMUI_NOTARY_ISSUER" \
    --wait
  xcrun stapler staple "$app"
  spctl --assess --type execute --verbose=2 "$app"
  rm -f "$archive"
  ditto -c -k --sequesterRsrc --keepParent "$app" "$archive"
fi

echo "$archive"
