#!/usr/bin/env bash
# Starts the GUI window, checks that the process stays up, then runs an optional
# capture command while the window is open. The overview is checked by the
# TestLiveOverview test in the workflow, not here.
#
# usage: gui-smoke.sh <binary> [capture command]
set -euo pipefail

bin=$1
capture=${2:-}

"$bin" > gui.log 2>&1 &
pid=$!
trap 'kill "$pid" 2>/dev/null || true' EXIT

# Give the page time to load its scripts and the first API calls.
sleep 8
kill -0 "$pid" || { cat gui.log; exit 1; }

if [ -n "$capture" ]; then
  $capture
fi
