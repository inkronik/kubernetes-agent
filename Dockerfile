# syntax=docker/dockerfile:1.28.0@sha256:bb22d9815c728170f72750f4e5b0d672e06176142e1d602c7e66c050100b7e5b

FROM golang:1.27.3-alpine3.24@sha256:b8c9b82affbbb82a020bbcf998dad2ba9516944ac7e883524642a481a9505e9c AS builder
RUN apk upgrade --no-cache
ARG TARGETOS=linux
ARG TARGETARCH
ARG VERSION=dev
WORKDIR /src

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY . .
RUN --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS="${TARGETOS}" GOARCH="${TARGETARCH}" \
    go build -trimpath \
    -ldflags="-s -w -X github.com/inkronik/kubernetes-agent/internal/version.Value=${VERSION}" \
    -o /out/inkronik-k8s-agent ./cmd/inkronik-k8s-agent

FROM alpine:3.24.3@sha256:be2656061b536c1883c4034790e21d0a09c5aaaf51968b89368a3c0371ea2c9a
RUN apk upgrade --no-cache
ARG VERSION=dev
RUN apk add --no-cache ca-certificates \
    && addgroup -g 65532 -S inkronik \
    && adduser -u 65532 -S -D -H -G inkronik inkronik
ENV INKRONIK_K8S_AGENT_VERSION=${VERSION}
COPY --from=builder --chown=65532:65532 /out/inkronik-k8s-agent /usr/local/bin/inkronik-k8s-agent
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/inkronik-k8s-agent"]
