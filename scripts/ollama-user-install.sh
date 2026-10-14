#!/usr/bin/env bash
# Installs Ollama for the current user (no root): binary + libraries in
# ~/.local/share/ollama, a systemd user service listening on the Docker bridge
# (172.17.0.1:11434, reachable from containers as host.docker.internal, not
# from the LAN), and the model the LLM agents use.
set -euo pipefail
DIR="$HOME/.local/share/ollama"
MODEL="${LLM_MODEL:-qwen3:1.7b}"
BRIDGE="$(ip -4 -o addr show docker0 | awk '{print $4}' | cut -d/ -f1)"
BRIDGE="${BRIDGE:-172.17.0.1}"

if [ ! -x "$DIR/bin/ollama" ]; then
  command -v zstd >/dev/null || { echo "zstd is required (sudo apt-get install zstd)"; exit 1; }
  mkdir -p "$DIR"
  echo "Downloading Ollama (~1.4 GB) into $DIR"
  curl -fsSL https://ollama.com/download/ollama-linux-amd64.tar.zst | zstd -d | tar -xf - -C "$DIR"
fi
"$DIR/bin/ollama" --version || true

mkdir -p "$HOME/.config/systemd/user"
cat > "$HOME/.config/systemd/user/ollama.service" <<UNIT
[Unit]
Description=Ollama (user service for agent-atlas, listens on the Docker bridge only)

[Service]
ExecStart=%h/.local/share/ollama/bin/ollama serve
Environment=OLLAMA_HOST=$BRIDGE:11434
Environment=OLLAMA_KEEP_ALIVE=30m
Environment=OLLAMA_CONTEXT_LENGTH=2048
Restart=always
RestartSec=5

[Install]
WantedBy=default.target
UNIT
systemctl --user daemon-reload
systemctl --user enable --now ollama.service
for _ in $(seq 1 20); do curl -sf "http://$BRIDGE:11434/api/version" >/dev/null && break; sleep 1; done
OLLAMA_HOST="$BRIDGE:11434" "$DIR/bin/ollama" pull "$MODEL"
OLLAMA_HOST="$BRIDGE:11434" "$DIR/bin/ollama" list
