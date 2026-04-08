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

# run_and_stream "Run 1: -path=specs/auto/aks-container-networking/azure-cns" -path=specs/auto/aks-container-networking/azure-cns
# run_and_stream "Run 2: -path=specs/auto/aks-container-networking/azure-cni" -path=specs/auto/aks-container-networking/azure-cni
# run_and_stream "Run 3: -path=specs/auto/aks-container-networking/azure-ipam" -path=specs/auto/aks-container-networking/azure-ipam
run_and_stream "Run 4: -path=specs/auto/aks-secure-tls-bootstrap" -path=specs/auto/aks-secure-tls-bootstrap
# run_and_stream "Run 5: -path=specs/auto/aks-node-controller" -path=specs/auto/aks-node-controller
# run_and_stream "Run Test: -path=specs/auto/test" -path=specs/auto/test
# run_and_stream "Run ?: -path=" -path=
exit $overall_exit