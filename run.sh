#!/usr/bin/env bash
set -euo pipefail

run_and_stream() {
  local label="$1"; shift
  local tmpfile
  tmpfile=$(mktemp)

  echo "========================================"
  echo "$label"
  echo "========================================"

  # Stream output in real time via tee; capture exit code from go run (not tee)
  go run . "$@" 2>&1 | tee "$tmpfile"; local pipe_exit=${PIPESTATUS[0]}

  local specPaths
  specPaths="$(grep '^specPaths=' "$tmpfile" | cut -d= -f2- || true)"
  rm -f "$tmpfile"

  echo "specPaths=${specPaths}"

  return $pipe_exit
}

# run_and_stream "Run 1: default (all onboard files)"

echo ""

run_and_stream "Run 2: -path=containernetworkingauto/azure-cns" -path=containernetworkingauto/azure-cns

echo ""

# run_and_stream "Run 3: -path=prometheus-collector" -path=prometheus-collector

echo ""

run_and_stream "Run 4: -path=containernetworkingauto/azure-cni" -path=containernetworkingauto/azure-cni
