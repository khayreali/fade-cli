#!/bin/sh
# Build the four self-contained binaries consumed by fade-cli update.
set -eu
mkdir -p dist
for platform in darwin_arm64 darwin_amd64 linux_arm64 linux_amd64; do
  target_os=${platform%_*}
  target_arch=${platform#*_}
  CGO_ENABLED=0 GOOS="$target_os" GOARCH="$target_arch" \
    go build -trimpath -ldflags '-s -w' -o "dist/fade-cli_$platform" ./cmd/fade-cli
done
cd dist
if command -v sha256sum >/dev/null 2>&1; then
  sha256sum fade-cli_darwin_arm64 fade-cli_darwin_amd64 fade-cli_linux_arm64 fade-cli_linux_amd64 > SHA256SUMS
else
  shasum -a 256 fade-cli_darwin_arm64 fade-cli_darwin_amd64 fade-cli_linux_arm64 fade-cli_linux_amd64 > SHA256SUMS
fi
