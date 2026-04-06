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

# run_and_stream "Run 1: -path=aks-container-networking/azure-cns" -path=aks-container-networking/azure-cns
# run_and_stream "Run 2: -path=aks-container-networking/azure-cni" -path=aks-container-networking/azure-cni
# run_and_stream "Run 3: -path=aks-container-networking/azure-ipam" -path=aks-container-networking/azure-ipam
run_and_stream "Run 4: -path=aks-secure-tls-bootstrap" -path=aks-secure-tls-bootstrap
# run_and_stream "Run 5: -path=aks-node-controller" -path=aks-node-controller
exit $overall_exit