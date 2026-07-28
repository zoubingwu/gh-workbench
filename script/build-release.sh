#!/usr/bin/env bash

set -euo pipefail

version="${1:?release version is required}"
platforms=(
  darwin-amd64
  darwin-arm64
  freebsd-386
  freebsd-amd64
  freebsd-arm64
  linux-386
  linux-amd64
  linux-arm
  linux-arm64
  windows-386
  windows-amd64
  windows-arm64
)

mkdir -p dist

for platform in "${platforms[@]}"; do
  goos="${platform%-*}"
  goarch="${platform#*-}"
  extension=""
  if [[ "$goos" == "windows" ]]; then
    extension=".exe"
  fi

  CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" go build \
    -trimpath \
    -ldflags="-s -w -X main.version=$version" \
    -o "dist/${platform}${extension}" \
    ./cmd/gh-workbench
done

cp LICENSE THIRD_PARTY_NOTICES.md dist/

host_goos="$(go env GOHOSTOS)"
host_goarch="$(go env GOHOSTARCH)"
host_platform="${host_goos}-${host_goarch}"
host_extension=""
if [[ "$host_goos" == "windows" ]]; then
  host_extension=".exe"
fi
actual_version="$(dist/${host_platform}${host_extension} --version)"
expected_version="gh-workbench ${version}"
if [[ "$actual_version" != "$expected_version" ]]; then
  printf 'version smoke test: got %q, want %q\n' \
    "$actual_version" \
    "$expected_version" >&2
  exit 1
fi
