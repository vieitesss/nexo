#!/usr/bin/env bash

tmux run-shell 'nexo tmux init --backend=tmux || tmux display-message "nexo: binary not found on PATH"'
