GOOS := $(shell go env GOOS)
GOARCH := $(shell go env GOARCH)
SKIP_INSTALL := false

# Build the CLI and Desktop
.PHONY: build
build:
	SKIP_INSTALL=$(SKIP_INSTALL) BUILD_PLATFORMS=$(GOOS) BUILD_ARCHS=$(GOARCH) ./hack/rebuild.sh

# Run the desktop app
.PHONY: run-desktop
run-desktop: build
	cd desktop && yarn desktop:dev:debug


