#!/usr/bin/env bash

set -e

BUILD_DIR="${BUILDDIR:=test}"
SRC_DIR="${SRCDIR:=.}"

# Create directory if it doesn't exist
if [ ! -d $BUILD_DIR ]
then
    mkdir ./$BUILD_DIR
fi

# Each build gets its own version, so that DevPod replaces the agent that an earlier build put into a container.
# The CLI and the agents it embeds must have the same version.
VERSION="${VERSION:-v0.0.0-dev.$(date -u +%Y%m%d%H%M%S)}"
LDFLAGS="-s -w -X github.com/loft-sh/devpod/pkg/version.version=$VERSION"
echo "Building version $VERSION"

# the CLI embeds the Linux agents
SRCDIR=$SRC_DIR LDFLAGS="$LDFLAGS" $SRC_DIR/hack/build-agents.sh

CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags "$LDFLAGS" -o $BUILD_DIR/devpod-linux-amd64 $SRC_DIR
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -ldflags "$LDFLAGS" -o $BUILD_DIR/devpod-linux-arm64 $SRC_DIR
CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -ldflags "$LDFLAGS" -o $BUILD_DIR/devpod-darwin-arm64 $SRC_DIR
CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -ldflags "$LDFLAGS" -o $BUILD_DIR/devpod-darwin-amd64 $SRC_DIR

chmod +x $BUILD_DIR/devpod-linux-amd64
chmod +x $BUILD_DIR/devpod-linux-arm64
chmod +x $BUILD_DIR/devpod-darwin-arm64
chmod +x $BUILD_DIR/devpod-darwin-amd64
