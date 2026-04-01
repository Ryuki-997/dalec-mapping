#!/usr/bin/env bash
set -uo pipefail

overall_exit=0

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

  if [ $pipe_exit -ne 0 ]; then
    echo "❌ $label failed with exit code $pipe_exit"
    overall_exit=1
  fi

  return 0
}

# run_and_stream "Run All Container-Networking Specs" -path=containernetworkingauto
# run_and_stream "Run 1: default (all onboard files)"
# run_and_stream "Run 3: -path=prometheus-collector" -path=prometheus-collector
# run_and_stream "Run 2: -path=containernetworkingauto/azure-cns" -path=containernetworkingauto/azure-cns
# run_and_stream "Run 4: -path=containernetworkingauto/azure-cni" -path=containernetworkingauto/azure-cni
# run_and_stream "Run 5: -path=containernetworkingauto/azure-ipam" -path=containernetworkingauto/azure-ipam
# run_and_stream "Run ADO: -path=containernetworkingauto/ado" -path=containernetworkingauto/ado
run_and_stream "Run Test PR: -path=containernetworkingauto/test-pr" -path=containernetworkingauto/test-pr
exit $overall_exit