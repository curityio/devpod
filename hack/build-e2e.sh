#!/usr/bin/env bash

set -e

BUILD_DIR="${BUILDDIR:=test}"
SRC_DIR="${SRCDIR:=.}"

# Create directory if it doesn't exist
if [ ! -d $BUILD_DIR ]
then
    mkdir ./$BUILD_DIR
fi

CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags "-s -w" -o $BUILD_DIR/devpod-linux-amd64 $SRC_DIR
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -ldflags "-s -w" -o $BUILD_DIR/devpod-linux-arm64 $SRC_DIR
CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -ldflags "-s -w" -o $BUILD_DIR/devpod-darwin-arm64 $SRC_DIR
CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -ldflags "-s -w" -o $BUILD_DIR/devpod-darwin-amd64 $SRC_DIR

chmod +x $BUILD_DIR/devpod-linux-amd64
chmod +x $BUILD_DIR/devpod-linux-arm64
chmod +x $BUILD_DIR/devpod-darwin-arm64
chmod +x $BUILD_DIR/devpod-darwin-amd64

# The CLI injects the agent from os.TempDir()/devpod-cache, and dev builds never refresh it,
# so put the new binaries where Go looks: $TMPDIR when set (as on macOS), /tmp otherwise.
CACHE_DIR="${TMPDIR:-/tmp}"
CACHE_DIR="${CACHE_DIR%/}/devpod-cache"
mkdir -p "$CACHE_DIR"
cp $BUILD_DIR/devpod-linux-amd64 "$CACHE_DIR/devpod-linux-amd64"
cp $BUILD_DIR/devpod-linux-arm64 "$CACHE_DIR/devpod-linux-arm64"
echo "Copied the linux agent binaries to $CACHE_DIR"
