#!/bin/sh
set -e
go build -o agent .
go build -o agent-restarter ./cmd/vk-gateway-restarter
