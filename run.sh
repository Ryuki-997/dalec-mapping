#!/usr/bin/env bash
set -euo pipefail

echo "========================================"
echo "Run 1: default (all onboard files)"
echo "========================================"
specPaths=""
output="$(go run . 2>&1)" && echo "$output" || { echo "$output"; exit 1; }
specPaths="$(echo "$output" | grep '^specPaths=' | cut -d= -f2- || true)"
echo "specPaths=${specPaths}"

echo ""
echo "========================================"
echo "Run 2: -path=containernetworkingauto"
echo "========================================"
specPaths=""
output="$(go run . -path=containernetworkingauto 2>&1)" && echo "$output" || { echo "$output"; exit 1; }
specPaths="$(echo "$output" | grep '^specPaths=' | cut -d= -f2- || true)"
echo "specPaths=${specPaths}"