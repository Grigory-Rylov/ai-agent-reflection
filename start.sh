#!/usr/bin/env bash
set -euo pipefail

SESSION_NAME="opencode-vk-gateway"
PROJECT_DIR="/home/orangepi/projects/py/opencode-vk-gateway"

if tmux has-session -t "$SESSION_NAME" 2>/dev/null; then
  echo "tmux session '$SESSION_NAME' already exists"
  exit 0
fi

tmux new-session -d -s "$SESSION_NAME" "cd '$PROJECT_DIR' && source venv/bin/activate && python gateway-restarter.py"
echo "Started tmux session '$SESSION_NAME'"
