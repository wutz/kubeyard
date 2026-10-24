#!/usr/bin/env bash
# Starts the GUI window, checks that its server returns the page and the process
# stays up, then runs an optional capture command while the window is open.
#
# usage: gui-smoke.sh <binary> [capture command]
set -euo pipefail

bin=$1
capture=${2:-}

"$bin" -addr 127.0.0.1:7791 > gui.log 2>&1 &
pid=$!
trap 'kill "$pid" 2>/dev/null || true' EXIT

for _ in $(seq 1 30); do
  curl -sf http://127.0.0.1:7791/ -o page.html && break
  sleep 1
done
grep -q 'kubeyard-token' page.html

# Give the page time to load its scripts and the first API calls.
sleep 8
kill -0 "$pid" || { cat gui.log; exit 1; }

if [ -n "$capture" ]; then
  $capture
fi
