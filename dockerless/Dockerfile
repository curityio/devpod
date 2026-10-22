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

# Create kaniko directory with world write permission to allow non root run
RUN --mount=from=busybox,dst=/usr/ ["busybox", "sh", "-c", "mkdir -p /.dockerless && chmod 777 /.dockerless"]

COPY --from=certs /etc/ssl/certs/ca-certificates.crt /.dockerless/ssl/certs/
COPY files/nsswitch.conf /etc/nsswitch.conf

ENV HOME=/root
ENV USER=root
ENV KANIKO_DIR=/.dockerless
ENV PATH=/usr/local/bin:/.dockerless:/.dockerless/bin
ENV SSL_CERT_DIR=/.dockerless/ssl/certs

COPY --from=build /dockerless /.dockerless/dockerless
COPY --from=busybox /bin /.dockerless/bin

RUN ["/.dockerless/bin/sh", "-c", "echo 'root:x:0:0:root:/root:/.dockerless/bin/sh' > /etc/passwd && chmod 666 /etc/passwd"]

WORKDIR /

ENTRYPOINT ["/.dockerless/bin/sh", "-c"]

CMD ["sleep infinity"]
