#!/usr/bin/env bash
set -uo pipefail

# ═══════════════════════════════════════════════════════════════════════════════
# test.sh — Run the pipeline against multiple onboard paths and verify each
# generated specfile against the expected golden files in ./correct/.
#
# Usage:
#   ./test.sh
#
# Expected golden files live under:
#   ./correct/{component}/{component}-v{version}-specfile.yml
#
# The Go pipeline compares each spec against its golden file immediately after
# generation (via diffWithGolden) and emits structured PASS/FAIL/SKIP lines.
# This script runs the pipeline, parses those lines, and reports a summary.
# ═══════════════════════════════════════════════════════════════════════════════

PATHS=(
    # "specs/containernetworking false"
    # "specs/aks-node-controller true"
    "specs/aks-secure-tls-bootstrap true"
)

rm -rf ./diff ./generated
mkdir -p ./diff ./generated

passed=0
failed=0
skipped=0
errors=""
overall_exit=0
results_file="${TMPDIR:-/tmp}/test_results_$$"
rm -f "$results_file"
trap 'rm -f "$results_file"' EXIT

for entry in "${PATHS[@]}"; do
    spec_path="${entry%% *}"
    force_flag="${entry##* }"

    force_arg=""
    if [[ "$force_flag" == "true" ]]; then
        force_arg="-force"
    fi

    echo "════════════════════════════════════════"
    echo "  Running pipeline: go run . -path=${spec_path} ${force_arg}"
    echo "════════════════════════════════════════"
    echo ""

    # Run the pipeline, streaming output and capturing diffWithGolden results.
    # diffWithGolden runs per component×tag right after spec generation,
    # so result/output.yml is always fresh when the comparison happens.
    go run . -path="${spec_path}" ${force_arg} 2>&1 | while IFS= read -r line; do
        echo "$line"

        # ✅ PASS  {component} @ {tag}
        if [[ "$line" == *"✅ PASS "* ]]; then
            label="${line#*PASS  }"
            echo "PASS:${label}" >> "$results_file"
        # ❌ FAIL  {component} @ {tag} — diff written to {path}
        elif [[ "$line" == *"❌ FAIL "* && "$line" == *"diff written"* ]]; then
            label="${line#*FAIL  }"
            echo "FAIL:${label}" >> "$results_file"
        # ⚠️  SKIP diff for {component} @ {tag} — no golden file at {path}
        elif [[ "$line" == *"SKIP diff"* && "$line" == *"⚠️"* ]]; then
            label="${line#*SKIP diff for }"
            echo "SKIP:${label}" >> "$results_file"
        fi
    done

    pipe_exit=${PIPESTATUS[0]}
    if [[ $pipe_exit -ne 0 ]]; then
        echo "❌ Pipeline failed for ${spec_path} (exit code ${pipe_exit})"
        overall_exit=1
    fi
    echo ""
done

# Tally and display each result from the temp file.
if [[ -f "$results_file" ]]; then
    passed=$(grep -c '^PASS:' "$results_file" 2>/dev/null || true)
    failed=$(grep -c '^FAIL:' "$results_file" 2>/dev/null || true)
    skipped=$(grep -c '^SKIP:' "$results_file" 2>/dev/null || true)
fi

echo ""
echo "════════════════════════════════════════"
echo "  Diff Results"
echo "════════════════════════════════════════"

if [[ -f "$results_file" ]]; then
    while IFS= read -r entry; do
        status="${entry%%:*}"
        detail="${entry#*:}"
        case "$status" in
            PASS)  echo "  ✅ PASS  ${detail}" ;;
            FAIL)  echo "  ❌ FAIL  ${detail}"
                   errors="${errors}  ${detail%%—*}\n" ;;
            SKIP)  echo "  ⚠️  SKIP  ${detail}" ;;
        esac
    done < "$results_file"
else
    echo "  (no diff results captured)"
fi

echo ""
echo "════════════════════════════════════════"
echo "  Summary"
echo "════════════════════════════════════════"
echo "  Passed:  ${passed}"
echo "  Failed:  ${failed}"
echo "  Skipped: ${skipped}"
if [[ $overall_exit -ne 0 ]]; then
    echo "  Pipeline exit code: ${overall_exit}"
fi
if [[ -n "$errors" ]]; then
    echo ""
    echo "  Failed specs:"
    echo -e "$errors"
fi
echo ""

if [[ $failed -gt 0 || $overall_exit -ne 0 ]]; then
    exit 1
fi
exit 0
