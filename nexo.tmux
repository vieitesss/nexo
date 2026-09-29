#!/usr/bin/env bash

# Keep adapter errors visible in tmux without making TPM treat a failed init as
# a missing binary. A missing executable is the only fallback condition.
tmux run-shell 'if ! command -v nexo >/dev/null 2>&1; then tmux display-message "nexo: binary not found on PATH"; exit 0; fi; if ! error=$(nexo tmux init --backend=tmux 2>&1); then tmux display-message "$error"; fi; exit 0'
