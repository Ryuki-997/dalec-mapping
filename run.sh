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


run_and_stream "Run ContainerNetworking: -path=specs/containernetworking" -path=specs/containernetworking
# run_and_stream "Run AKS-Secure-TLS-Bootstrap: -path=specs/aks-secure-tls-bootstrap" -path=specs/aks-secure-tls-bootstrap
# run_and_stream "Run AKS-Node-Controller: -path=specs/aks-node-controller" -path=specs/aks-node-controller

## Project Dalec Components

## Different Org Components
# run_and_stream "Run abc: -path=autospecs/service-hub" -path=autospecs/service-hub

exit $overall_exit