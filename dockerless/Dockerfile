# Built and published as ghcr.io/curityio/dockerless by .github/workflows/dockerless.yaml.
FROM --platform=$BUILDPLATFORM golang:1.27.2-alpine AS build
ARG TARGETOS
ARG TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags "-s -w" -o /dockerless .

FROM alpine:3.23 AS certs
RUN apk add --no-cache ca-certificates

# use musl busybox since it's staticly compiled on all platforms
FROM busybox:1.37.0-musl AS busybox

# now build from scratch
FROM scratch

# Links the package to this repository when it is first published to ghcr.io, so that it gets the
# repository's access permissions (not its visibility, which is set in the package's settings).
LABEL org.opencontainers.image.source=https://github.com/curityio/devpod

# Only root may write to the kaniko directory: dockerless and DevPod's agent run as root, and the
# container's other users must not be able to put files where root runs them.
RUN --mount=from=busybox,dst=/usr/ ["busybox", "sh", "-c", "mkdir -p /.dockerless && chmod 755 /.dockerless"]

COPY --from=certs /etc/ssl/certs/ca-certificates.crt /.dockerless/ssl/certs/
COPY files/nsswitch.conf /etc/nsswitch.conf

# Keep these in step with dockerlessEnv in cmd/start.go.
ENV HOME=/root
ENV USER=root
ENV KANIKO_DIR=/.dockerless
# DevPod runs commands in the container through `sh` and `su`, as root, so PATH holds no directory
# a user could write to. dockerless itself is always called by its full path.
ENV PATH=/usr/local/bin:/.dockerless/bin
ENV SSL_CERT_DIR=/.dockerless/ssl/certs

COPY --from=build /dockerless /.dockerless/dockerless
COPY --from=busybox /bin /.dockerless/bin

RUN ["/.dockerless/bin/sh", "-c", "echo 'root:x:0:0:root:/root:/.dockerless/bin/sh' > /etc/passwd && chmod 666 /etc/passwd"]

WORKDIR /

ENTRYPOINT ["/.dockerless/bin/sh", "-c"]

CMD ["sleep infinity"]
