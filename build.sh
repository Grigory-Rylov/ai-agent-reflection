#!/bin/sh
set -e
go build -o agent .
go build -o restarter ./cmd/vk-gateway-restarter
