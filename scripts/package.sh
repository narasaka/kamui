#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 1 || -z "$1" ]]; then
  echo "usage: scripts/package.sh VERSION" >&2
  exit 2
fi

version="$1"
commit="${KAMUI_BUILD_COMMIT:-$(git rev-parse HEAD)}"
build_date="${KAMUI_BUILD_DATE:-unknown}"
output_root="dist"
ldflags="-s -w -buildid= -X github.com/narasaka/kamui/internal/version.Version=${version} -X github.com/narasaka/kamui/internal/version.Commit=${commit} -X github.com/narasaka/kamui/internal/version.BuildDate=${build_date}"

mkdir -p "$output_root"
for architecture in arm64 amd64; do
  output="${output_root}/kamui_${version}_darwin_${architecture}"
  CGO_ENABLED=0 GOOS=darwin GOARCH="$architecture" \
    go build -trimpath -buildvcs=false -ldflags "$ldflags" -o "$output" ./cmd/kamui
done

(
  cd "$output_root"
  openssl dgst -sha256 -r "kamui_${version}_darwin_arm64" "kamui_${version}_darwin_amd64" > checksums.txt
)
