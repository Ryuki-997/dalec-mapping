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

  if [ $pipe_exit -ne 0 ]; then
    echo "❌ $label failed with exit code $pipe_exit"
    overall_exit=1
  fi

  return 0
}

# run_and_stream "Run 1: -path=autospecs/aks-container-networking/azure-cns" -path=autospecs/aks-container-networking/azure-cns
# run_and_stream "Run 2: -path=autospecs/aks-container-networking/azure-cni" -path=autospecs/aks-container-networking/azure-cni
# run_and_stream "Run 3: -path=autospecs/aks-container-networking/azure-ipam" -path=autospecs/aks-container-networking/azure-ipam
# run_and_stream "Run 4: -path=autospecs/aks-secure-tls-bootstrap" -path=autospecs/aks-secure-tls-bootstrap
# run_and_stream "Run 5: -path=autospecs/aks-node-controller" -path=autospecs/aks-node-controller
# run_and_stream "Run 6: -path=autospecs/aks-vm-extension" -path=autospecs/aks-vm-extension

## Test Runs
run_and_stream "Run AKS-Container-Networking: -path=autospecs/test/azure-cns" -path=autospecs/test/azure-cns
run_and_stream "Run AKS-Secure-TLS-Bootstrap: -path=autospecs/test/aks-secure-tls-bootstrap" -path=autospecs/test/aks-secure-tls-bootstrap
# run_and_stream "Run AKS-Node-Controller: -path=autospecs/test/aks-node-controller" -path=autospecs/test/aks-node-controller
# run_and_stream "Run AKS-VM-Extension: -path=autospecs/test/node-problem-detector" -path=autospecs/test/node-problem-detector

## Project Dalec Components
# run_and_stream "Run Addon-Resizer: -path=autospecs/addon-resizer" -path=autospecs/addon-resizer

## Different Org Components
# run_and_stream "Run abc: -path=autospecs/service-hub" -path=autospecs/service-hub

exit $overall_exit