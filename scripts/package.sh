#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 1 || -z "$1" ]]; then
  echo "usage: scripts/package.sh VERSION" >&2
  exit 2
fi

version="$1"
commit="${KAMUI_BUILD_COMMIT:-$(git rev-parse HEAD)}"
build_date="${KAMUI_BUILD_DATE:-unknown}"
output_root="${KAMUI_OUTPUT_ROOT:-dist}"
ldflags="-s -w -buildid= -X github.com/narasaka/kamui/internal/version.Version=${version} -X github.com/narasaka/kamui/internal/version.Commit=${commit} -X github.com/narasaka/kamui/internal/version.BuildDate=${build_date}"

mkdir -p "$output_root"
artifacts=()
for operating_system in darwin linux; do
  for architecture in arm64 amd64; do
    artifact="kamui_${version}_${operating_system}_${architecture}"
    output="${output_root}/${artifact}"
    CGO_ENABLED=0 GOOS="$operating_system" GOARCH="$architecture" \
      go build -trimpath -buildvcs=false -ldflags "$ldflags" -o "$output" ./cmd/kamui
    artifacts+=("$artifact")
  done
done

(
  cd "$output_root"
  openssl dgst -sha256 -r "${artifacts[@]}" > checksums.txt
)
