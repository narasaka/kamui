#!/usr/bin/env bash
# Builds the universal Kamui menu bar app and its release archive.
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

package_root="${repository_root}/macos"
swift build --package-path "$package_root" -c release --arch arm64 --arch x86_64 --product KamuiMenuBar
binary="$(swift build --package-path "$package_root" -c release --arch arm64 --arch x86_64 --show-bin-path)/KamuiMenuBar"

app="${output_root}/Kamui.app"
rm -rf "$app"
mkdir -p "${app}/Contents/MacOS" "${app}/Contents/Resources"
cp "$binary" "${app}/Contents/MacOS/Kamui"
cp "${package_root}/Resources/AppIcon.icns" "${app}/Contents/Resources/AppIcon.icns"
sed "s/KAMUI_VERSION/${version}/g" "${package_root}/Resources/Info.plist" > "${app}/Contents/Info.plist"
plutil -lint "${app}/Contents/Info.plist" >/dev/null

if [[ "$identity" == "-" ]]; then
  codesign --force --sign - "$app"
else
  codesign --force --options runtime --timestamp --sign "$identity" "$app"
fi
codesign --verify --strict --verbose=2 "$app"

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
