#!/bin/sh
set -ex
export PATH="${PATH}:/usr/local/go/bin"
go build -o agent .
go build -o agent-restarter ./cmd/vk-gateway-restarter
