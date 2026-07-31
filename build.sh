#!/bin/sh
set -ex
export PATH="${PATH}:/usr/local/go/bin"
BUILD_TIME="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
LDFLAGS="-X github.com/opencode/llama-client/pkg/buildinfo.BuildTime=${BUILD_TIME}"
go build -ldflags "${LDFLAGS}" -o agent .
go build -ldflags "${LDFLAGS}" -o agent-restarter ./cmd/vk-gateway-restarter
