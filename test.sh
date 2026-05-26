#!/usr/bin/env bash
set -uo pipefail

# ═══════════════════════════════════════════════════════════════════════════════
# test.sh — Run the pipeline against multiple onboard paths and verify each
# generated specfile against the expected golden files in ./correct/.
#
# Usage:
#   ./test.sh
#
# Each test section specifies:
#   path|force|expected_action
#
# The Go pipeline compares each spec against its golden file immediately after
# generation (via diffWithGolden) and emits structured PASS/FAIL/SKIP lines
# that include the action taken (e.g. [GENERATE], [BUMP VERSION]).
# This script runs the pipeline, parses those lines, validates that the action
# matches the expected action for that section, and reports a summary.
# ═══════════════════════════════════════════════════════════════════════════════

# Format: "path|force|expected_action"
SECTIONS=(
    "e2e-tests/github|false|GENERATE"
    "unit-tests/bump-version-action|false|BUMP VERSION"
    "unit-tests/bump-revision-action|false|BUMP REVISION"
    "unit-tests/tag-fetching|false|TAG_CHECK"
)

# Per-component overrides: when a component@tag matches a pattern below,
# override the section-level expected action with the specified one.
# Format: "section|glob_pattern|expected_action|check_mode"  (check_mode: full or action-only)
# Use "*" as section to match all sections.
ACTION_OVERRIDES=(
    "*|azure-cni @ v1.8.5*|GENERATE|action-only"
    "*|azure-cni @ v1.8.6*|GENERATE|action-only"
    "*|azure-cns @ v1.8.5*|GENERATE|action-only"
    "*|azure-cns @ v1.8.6*|GENERATE|action-only"
    "unit-tests/bump-revision-action|aks-node-controller @ v0.0.1*|SKIPPED|action-only"
    "unit-tests/bump-version-action|aks-secure-tls-bootstrap-client @ v1.0.3*|SKIPPED|action-only"
)

# Expected versions for TAG_CHECK sections.
# Components listed here must have the special version; all others use the default.
TAG_CHECK_SPECIAL_COMPONENTS=(
    "hub-net-controller-manager"
    "member-net-controller-manager"
    "mcs-controller-manager"
    "net-crd-installer"
)
TAG_CHECK_SPECIAL_VERSION="v0.3.33"
TAG_CHECK_DEFAULT_VERSION="v0.18.5"

rm -rf ./diff ./generated
mkdir -p ./diff ./generated

overall_exit=0
results_file="${TMPDIR:-/tmp}/test_results_$$"
rm -f "$results_file"
trap 'rm -f "$results_file"' EXIT

for section in "${SECTIONS[@]}"; do
    IFS='|' read -r spec_path force_flag expected_action <<< "$section"

    force_arg=""
    if [[ "$force_flag" == "true" ]]; then
        force_arg="-force"
    fi

    echo "════════════════════════════════════════"
    echo "  Section: ${spec_path} (expected action: ${expected_action})"
    echo "  Running: go run . -branch=SpecfileTest -path=${spec_path} ${force_arg}"
    echo "════════════════════════════════════════"
    echo ""

    # Run the pipeline, streaming output and capturing diffWithGolden results.
    # diffWithGolden runs per component×tag right after spec generation,
    # so result/output.yml is always fresh when the comparison happens.
    go run . -branch=SpecfileTest -path="${spec_path}" ${force_arg} 2>&1 | while IFS= read -r line; do
        echo "$line"

        # ✅ PASS  {component} @ {tag} [{action}]
        if [[ "$line" == *"✅ PASS "* ]]; then
            label="${line#*PASS  }"
            echo "PASS:${spec_path}:${expected_action}:${label}" >> "$results_file"
        # ❌ FAIL  {component} @ {tag} [{action}] — diff written to {path}
        elif [[ "$line" == *"❌ FAIL "* && "$line" == *"diff written"* ]]; then
            label="${line#*FAIL  }"
            echo "FAIL:${spec_path}:${expected_action}:${label}" >> "$results_file"
        # ⚠️  SKIP diff for {component} @ {tag} [{action}] — no golden file at {path}
        elif [[ "$line" == *"SKIP diff"* && "$line" == *"⚠️"* ]]; then
            label="${line#*SKIP diff for }"
            echo "SKIP:${spec_path}:${expected_action}:${label}" >> "$results_file"
        fi
    done

    pipe_exit=${PIPESTATUS[0]}
    if [[ $pipe_exit -ne 0 ]]; then
        echo "❌ Pipeline failed for ${spec_path} (exit code ${pipe_exit})"
        overall_exit=1
    fi
    echo ""
done

echo ""
echo "════════════════════════════════════════"
echo "  Diff Results"
echo "════════════════════════════════════════"

if [[ -f "$results_file" ]]; then
    while IFS= read -r entry; do
        status="${entry%%:*}"
        rest="${entry#*:}"
        # skip section field
        rest="${rest#*:}"
        expected="${rest%%:*}"
        detail="${rest#*:}"

        case "$status" in
            PASS)  echo "  ✅ PASS  ${detail}" ;;
            FAIL)  echo "  ❌ FAIL  ${detail}" ;;
            SKIP)  echo "  ⚠️  SKIP  ${detail}" ;;
        esac
    done < "$results_file"
else
    echo "  (no diff results captured)"
fi

# ── Test Verdicts ──
# Apply pass/fail logic based on expected action per section:
#   GENERATE:     PASS = golden matches, FAIL/SKIP = test failure
#   BUMP VERSION: correct action = pass (no golden comparison), wrong action = fail
test_passed=0
test_failed=0

# Column widths for table formatting
col_result=6    # ✅/❌
col_comp=40
col_ver=12
col_action=16
col_expected=16

print_table_header() {
    printf "  %-${col_result}s %-${col_comp}s %-${col_ver}s %-${col_action}s %s\n" \
        "" "COMPONENT" "VERSION" "ACTION" "EXPECTED"
    printf "  %-${col_result}s %-${col_comp}s %-${col_ver}s %-${col_action}s %s\n" \
        "" "$(printf '%0.s─' {1..39})" "$(printf '%0.s─' {1..11})" "$(printf '%0.s─' {1..15})" "$(printf '%0.s─' {1..15})"
}

print_table_row() {
    local icon="$1" comp_name="$2" version="$3" action="$4" expected="$5"
    printf "  %-${col_result}s %-${col_comp}s %-${col_ver}s %-${col_action}s %s\n" \
        "$icon" "$comp_name" "$version" "$action" "$expected"
}

echo ""
echo "════════════════════════════════════════════════════════════════════════════════════════════"
echo "  Test Results"
echo "════════════════════════════════════════════════════════════════════════════════════════════"

if [[ -f "$results_file" ]]; then
    # ── Collect all unique sections in order ──
    declare -a seen_sections=()
    while IFS= read -r entry; do
        rest="${entry#*:}"
        section="${rest%%:*}"
        already_seen=false
        for s in "${seen_sections[@]+"${seen_sections[@]}"}"; do
            if [[ "$s" == "$section" ]]; then already_seen=true; break; fi
        done
        if [[ "$already_seen" == false ]]; then
            seen_sections+=("$section")
        fi
    done < "$results_file"

    # ── Process each section ──
    for current_section in "${seen_sections[@]}"; do
        echo "  ── ${current_section} ──"
        print_table_header

        # Collect rows for this section into a temp file for sorting
        section_rows="${TMPDIR:-/tmp}/test_section_rows_$$"
        rm -f "$section_rows"

        while IFS= read -r entry; do
            status="${entry%%:*}"
            rest="${entry#*:}"
            section="${rest%%:*}"
            rest="${rest#*:}"
            expected="${rest%%:*}"
            detail="${rest#*:}"

            [[ "$section" != "$current_section" ]] && continue

            # Extract actual action from detail: "component @ tag [ACTION] ..."
            actual_action=""
            if [[ "$detail" == *"["*"]"* ]]; then
                actual_action="${detail#*[}"
                actual_action="${actual_action%%]*}"
            fi

            # Extract component name and version from detail: " component @ tag [...]"
            comp_name="${detail%% @*}"
            comp_name="${comp_name# }"
            actual_version="${detail#* @ }"
            actual_version="${actual_version%% \[*}"
            actual_version="${actual_version%% —*}"

            # Check per-component overrides before using section-level expected action
            effective_expected="$expected"
            check_mode="full"
            comp_label="${detail%% \[*}"
            comp_label="${comp_label%% —*}"
            for override in "${ACTION_OVERRIDES[@]}"; do
                override_section="${override%%|*}"
                override_rest="${override#*|}"
                override_pattern="${override_rest%%|*}"
                override_rest="${override_rest#*|}"
                override_action="${override_rest%%|*}"
                override_mode="${override_rest#*|}"
                if [[ "$override_mode" == "$override_action" ]]; then
                    override_mode="full"
                fi
                if [[ "$override_section" != "*" && "$override_section" != "$section" ]]; then
                    continue
                fi
                if [[ "$comp_label" == $override_pattern ]]; then
                    effective_expected="$override_action"
                    check_mode="$override_mode"
                    break
                fi
            done

            # Determine icon and display expected
            icon=""
            display_expected=""

            if [[ "$effective_expected" == "GENERATE" ]]; then
                if [[ "$check_mode" == "action-only" ]]; then
                    if [[ "$actual_action" == "$effective_expected" ]]; then
                        icon="✅"; display_expected="$effective_expected"
                        test_passed=$((test_passed + 1))
                    else
                        icon="❌"; display_expected="$effective_expected"
                        test_failed=$((test_failed + 1))
                    fi
                elif [[ "$status" == "PASS" && "$actual_action" == "$effective_expected" ]]; then
                    icon="✅"; display_expected="GOLDEN MATCH"
                    test_passed=$((test_passed + 1))
                elif [[ "$actual_action" != "$effective_expected" ]]; then
                    icon="❌"; display_expected="$effective_expected"
                    test_failed=$((test_failed + 1))
                elif [[ "$status" == "FAIL" ]]; then
                    icon="❌"; display_expected="GOLDEN MATCH"
                    test_failed=$((test_failed + 1))
                elif [[ "$status" == "SKIP" ]]; then
                    icon="❌"; display_expected="GOLDEN MATCH"
                    test_failed=$((test_failed + 1))
                fi
            elif [[ "$effective_expected" == "BUMP VERSION" || "$effective_expected" == "BUMP REVISION" || "$effective_expected" == "SKIPPED" ]]; then
                display_expected="$effective_expected"
                if [[ "$actual_action" == "$effective_expected" ]]; then
                    icon="✅"
                    test_passed=$((test_passed + 1))
                else
                    icon="❌"
                    test_failed=$((test_failed + 1))
                fi
            elif [[ "$effective_expected" == "TAG_CHECK" ]]; then
                expected_version="$TAG_CHECK_DEFAULT_VERSION"
                for special_comp in "${TAG_CHECK_SPECIAL_COMPONENTS[@]}"; do
                    if [[ "$comp_name" == "$special_comp" ]]; then
                        expected_version="$TAG_CHECK_SPECIAL_VERSION"
                        break
                    fi
                done
                display_expected="ver=${expected_version}"
                if [[ "$actual_version" == "$expected_version" ]]; then
                    icon="✅"
                    test_passed=$((test_passed + 1))
                else
                    icon="❌"
                    test_failed=$((test_failed + 1))
                fi
            fi

            # Write sort key (component name + version) and formatted row
            # Using tab as field separator: comp_name \t version \t formatted_row
            printf '%s\t%s\t' "$comp_name" "$actual_version" >> "$section_rows"
            printf '%-6s %-40s %-12s %-16s %s\n' \
                "$icon" "$comp_name" "$actual_version" "$actual_action" "$display_expected" >> "$section_rows"
        done < "$results_file"

        # Sort by component name (field 1) then version (field 2, version sort) and print
        if [[ -f "$section_rows" ]]; then
            sort -t$'\t' -k1,1 -k2,2V "$section_rows" | while IFS=$'\t' read -r _comp _ver row; do
                echo "  $row"
            done
            rm -f "$section_rows"
        fi

        echo ""
    done
else
    echo "  (no results captured)"
fi

echo ""
echo "════════════════════════════════════════════════════════════════════════════════════════════"
echo "  Summary: ${test_passed} passed, ${test_failed} failed"
if [[ $overall_exit -ne 0 ]]; then
    echo "  Pipeline exit code: ${overall_exit}"
fi
echo "════════════════════════════════════════════════════════════════════════════════════════════"
echo ""

if [[ $test_failed -gt 0 || $overall_exit -ne 0 ]]; then
    exit 1
fi
exit 0
