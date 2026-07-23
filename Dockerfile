# syntax=docker/dockerfile:1.7

ARG GO_VERSION=1.25.12
ARG ALPINE_VERSION=3.24.1

FROM golang:${GO_VERSION}-alpine3.24 AS builder
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

FROM alpine:${ALPINE_VERSION}
ARG VERSION=dev
RUN apk add --no-cache ca-certificates \
    && addgroup -g 65532 -S inkronik \
    && adduser -u 65532 -S -D -H -G inkronik inkronik
ENV INKRONIK_K8S_AGENT_VERSION=${VERSION}
COPY --from=builder --chown=65532:65532 /out/inkronik-k8s-agent /usr/local/bin/inkronik-k8s-agent
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/inkronik-k8s-agent"]
