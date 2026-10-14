# Ollama on this machine

The LLM-backed agents (one per scenario, always the streaming agent) call the gateway's LLM route
(`http://agentgateway:3100/v1/chat/completions`), which forwards to Ollama on the host. When Ollama is
not reachable, the agents report `llm_unavailable` and use their scripted answer; runs still complete.

## Installation (no root)

`make ollama-install` runs `scripts/ollama-user-install.sh`:

- Downloads the official Linux archive into `~/.local/share/ollama` (about 1.4 GB, needs `zstd`).
- Creates the systemd **user** service `~/.config/systemd/user/ollama.service`.
- Binds Ollama to the Docker bridge address (`172.17.0.1:11434`). Containers reach it as
  `host.docker.internal`; the LAN does not. The `ollama` CLI therefore needs
  `OLLAMA_HOST=172.17.0.1:11434`, which the Makefile targets set.
- Pulls the model (`LLM_MODEL`, default `qwen3:1.7b`, 1.4 GB).

A root install with the official script was not used: running `sudo` from the agent session is not
permitted, and the user-level install needs no privileges. A user service runs while you are logged in.

```bash
make ollama-check   # service state, models, CPU or GPU
make llm-check      # one completion through the gateway's LLM route
systemctl --user restart ollama
journalctl --user -u ollama -f
```

## Measurements (2026-10-14, Ollama 0.40.4, qwen3:1.7b)

Machine: Intel Core Ultra 5 335 (8 cores), Intel Graphics (Panther Lake) iGPU, 30 GB RAM.
Same prompt as the service-case agent, thinking off, three runs after a warm-up:

| backend | generation speed | answer of ~45 tokens |
|---|---|---|
| CPU | 26.3 tokens/s | 1.7 s |
| iGPU via Vulkan (`OLLAMA_IGPU_ENABLE=1`) | 22.4 tokens/s | 2.1 s |

The CPU is faster, so the iGPU stays off (Ollama's default for integrated GPUs). With five scenarios
running at once, the requests queue (`OLLAMA_NUM_PARALLEL=1`); the last answer arrives after about
10 s, well inside the agents' 45 s timeout.

## Thinking must be turned off

qwen3 is a reasoning model. On Ollama's OpenAI-compatible endpoint the `/no_think` prompt suffix is
ignored: the model spends the whole token budget on hidden reasoning and returns an empty answer. The
agents send `"reasoning_effort": "none"` (agentgateway passes it through), which brings an answer down
from 6–9 s (truncated) to about 2 s.
