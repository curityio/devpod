#!/usr/bin/env bash

# Builds the Linux agent binaries (amd64 and arm64) that get embedded into the devpod CLI, see pkg/agent/agentbin.
# Run it before building the CLI, with the same LDFLAGS (the version especially), so that the agent matches the CLI.
# Without LDFLAGS, the agents have version v0.0.0, like a plain `go build` of the CLI.

set -e

SRC_DIR="${SRCDIR:=.}"
LDFLAGS="${LDFLAGS:=-s -w}"
OUT_DIR="$SRC_DIR/pkg/agent/agentbin/bin"

for arch in amd64 arm64; do
    echo "[INFO] Building agent for linux/$arch"
    CGO_ENABLED=0 GOOS=linux GOARCH=$arch go build -tags devpod_agent -ldflags "$LDFLAGS" -o "$OUT_DIR/devpod-linux-$arch" "$SRC_DIR"
    gzip -9 -n -f "$OUT_DIR/devpod-linux-$arch"
done
echo "[INFO] Built agents in $OUT_DIR"
