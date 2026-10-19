#!/usr/bin/env bash

set -e

if [[ -z "${BUILD_PLATFORMS}" ]]; then
    BUILD_PLATFORMS="linux windows darwin"
fi

if [[ -z "${BUILD_ARCHS}" ]]; then
    BUILD_ARCHS="amd64 arm64"
fi

# Each build gets its own version, so that DevPod replaces the agent that an earlier build put into a container.
# The CLI and the agents it embeds must have the same version.
VERSION="${VERSION:-v0.0.0-dev.$(date -u +%Y%m%d%H%M%S)}"
LDFLAGS="-s -w -X github.com/loft-sh/devpod/pkg/version.version=$VERSION"
echo "[INFO] Building version $VERSION"

# the CLI embeds the Linux agents, so they're always needed
LDFLAGS="$LDFLAGS" ./hack/build-agents.sh

for os in $BUILD_PLATFORMS; do
    for arch in $BUILD_ARCHS; do
        # don't build for arm on windows
        if [[ "$os" == "windows" && "$arch" == "arm64" ]]; then
            continue
        fi
        echo "[INFO] Building for $os/$arch"
        if [[ $RACE == "yes" ]]; then
            echo "Building devpod with race detector"
            CGO_ENABLED=1 GOOS=$os GOARCH=$arch go build -race -ldflags "$LDFLAGS" -o test/devpod-cli-$os-$arch
        else
            CGO_ENABLED=0 GOOS=$os GOARCH=$arch go build -ldflags "$LDFLAGS" -o test/devpod-cli-$os-$arch
        fi
    done
done
echo "[INFO] Built binaries for all platforms in test/ directory"

# if not true, install the binary
if [[ "${SKIP_INSTALL}" != "true" ]]; then
    if command -v sudo &> /dev/null; then
        go build -ldflags "$LDFLAGS" -o test/devpod && sudo mv test/devpod /usr/local/bin/
    else 
        go install -ldflags "$LDFLAGS" .
    fi
    echo "[INFO] Installed devpod binary to /usr/local/bin"
else 
    echo "[INFO] Skipping install of devpod binary"
fi

if [ -d "desktop/src-tauri/bin" ]; then
    if [[ $BUILD_PLATFORMS == *"linux"* ]]; then
        cp test/devpod-cli-linux-amd64 desktop/src-tauri/bin/devpod-cli-x86_64-unknown-linux-gnu
        cp test/devpod-cli-linux-arm64 desktop/src-tauri/bin/devpod-cli-aarch64-unknown-linux-gnu
    fi
    if [[ $BUILD_PLATFORMS == *"windows"* ]]; then
        cp test/devpod-cli-windows-amd64 desktop/src-tauri/bin/devpod-cli-x86_64-pc-windows-msvc.exe
    fi
    if [[ $BUILD_PLATFORMS == *"darwin"* ]]; then
        cp test/devpod-cli-darwin-amd64 desktop/src-tauri/bin/devpod-cli-x86_64-apple-darwin
        cp test/devpod-cli-darwin-arm64 desktop/src-tauri/bin/devpod-cli-aarch64-apple-darwin
    fi
echo "[INFO] Copied binaries to desktop/src-tauri/bin"
fi
