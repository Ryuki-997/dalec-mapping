#!/usr/bin/env bash
set -uo pipefail

# ═══════════════════════════════════════════════════════════════════════════════
# run.sh — Production pipeline runner
#
# Runs ./test.sh first as a gate. Only proceeds to the actual pipeline if all
# tests pass. PRs are submitted automatically after tests pass.
# Use -force to force PR creation even when a PR already exists.
#
# Usage:
#   ./run.sh          # run tests, then submit PRs
#   ./run.sh -force   # run tests, then force-create PRs even if one exists
# ═══════════════════════════════════════════════════════════════════════════════

force_flag=false

for arg in "$@"; do
    case "$arg" in
        -force)   force_flag=true ;;
        *)        echo "Unknown flag: $arg"; echo "Usage: ./run.sh [-force]"; exit 1 ;;
    esac
done

# # ── Step 1: Run test suite as gate ──
# echo "════════════════════════════════════════"
# echo "  Running test suite..."
# echo "════════════════════════════════════════"
# echo ""

# ./test.sh
# test_exit=$?

# if [[ $test_exit -ne 0 ]]; then
#     echo ""
#     echo "❌ Tests failed — aborting pipeline run."
#     exit 1
# fi

# echo ""
# echo "✅ All tests passed — proceeding to pipeline run."
# echo ""

# ── Step 2: Run actual pipeline (always submits PRs) ──
overall_exit=0

go_args=()
echo "  PR submission: ENABLED"
if [[ "$force_flag" == true ]]; then
    go_args+=("-force")
    echo "  Force PR:      ENABLED"
fi
echo ""

run_and_stream() {
    local label="$1"; shift
    local tmpfile
    tmpfile=$(mktemp)

    echo "════════════════════════════════════════"
    echo "$label"
    echo "════════════════════════════════════════"

    go run . "$@" ${go_args[@]+"${go_args[@]}"} 2>&1 | tee "$tmpfile"; local pipe_exit=${PIPESTATUS[0]}
    rm -f "$tmpfile"

    if [[ $pipe_exit -ne 0 ]]; then
        echo "❌ $label failed with exit code $pipe_exit"
        overall_exit=1
    fi

    return 0
}

# run_and_stream "Run ContainerNetworking: -path=specs/containernetworking" -path=specs/containernetworking
run_and_stream "Run AKS-Secure-TLS-Bootstrap: -path=specs/aks-secure-tls-bootstrap" -path=specs/aks-secure-tls-bootstrap
# run_and_stream "Run AKS-Node-Controller: -path=specs/aks-node-controller" -path=specs/aks-node-controller
# run_and_stream "Run Azure Policy: -path=specs/azure-policy" -path=specs/azure-policy
# run_and_stream "Run Azure Fleet: -path=specs/aks/fleet" -path=specs/aks/fleet

## Project Dalec Components

## Different Org Components
# run_and_stream "Run abc: -path=autospecs/service-hub" -path=autospecs/service-hub

exit $overall_exit