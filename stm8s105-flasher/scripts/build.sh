#!/bin/sh
# Cross-compile the flasher for Windows and macOS.
set -eu
cd "$(dirname "$0")/.."
mkdir -p dist
build() {
  os=$1
  arch=$2
  out=$3
  echo "build $out"
  GOOS=$os GOARCH=$arch CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o "$out" ./cmd/stm8s105flash
}
build windows amd64 dist/stm8s105flash-windows-amd64.exe
build windows arm64 dist/stm8s105flash-windows-arm64.exe
build darwin amd64 dist/stm8s105flash-macos-amd64
build darwin arm64 dist/stm8s105flash-macos-arm64
build linux amd64 dist/stm8s105flash-linux-amd64
echo "输出在 dist/"
