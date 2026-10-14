#!/usr/bin/env bash
# Infra regression tests that need a fresh clone of the committed tree.
#
# R1 (review 2026-10-14): on a fresh clone the visualizer's bind mount
# ./data/sessions must exist and be writable by the user the visualizer runs
# as. Otherwise Docker creates it as root and the visualizer crash-loops.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
# A "fresh clone" of the working tree: exactly the files git would commit
# (tracked + new, not ignored), so the test also sees uncommitted fixes.
mkdir -p "$TMP/repo"
(cd "$ROOT" && git ls-files -z --cached --others --exclude-standard | tar --null -T - -cf -) | tar -xf - -C "$TMP/repo"
cd "$TMP/repo"
cp .env.example .env

fail() { echo "FAIL: $*"; exit 1; }

# The user compose runs the visualizer as (resolved the same way `make up` does).
user=$(make -s print-UID_GID)
[ "$user" = "$(id -u):$(id -g)" ] || fail "visualizer runs as '$user', expected the current user $(id -u):$(id -g)"

# What `make up` prepares before compose starts.
make -s prepare-dirs
[ -d data/sessions ] || fail "data/sessions does not exist in a fresh clone"
docker run --rm --entrypoint touch -u "$user" -v "$PWD/data/sessions:/data/sessions" \
  agent-atlas/visualizer:dev /data/sessions/probe || fail "the visualizer user cannot write data/sessions"
echo "ok: fresh clone, visualizer can write data/sessions as $user"
