#!/usr/bin/env bash
# ═══════════════════════════════════════════════════════════════════════════════
# test.sh — Decision-tree integration tests for the dalec-mapping pipeline.
#
# Uses specs/containernetworkingauto/test-pr/ in aks-dalec-build-defs as the
# test fixture.  Tests run sequentially — each builds on the prior state.
#
#   Test 1  First-time onboard  → actionNotify → PR created
#   Test 2  Rerun, no new tag   → skip (no actionable tags)
#   Test 3  Tag bump v1.6.42    → actionBumpCommit or actionNotify (PR)
#
# Prerequisites: gh CLI authenticated, local clone of aks-dalec-build-defs
# ═══════════════════════════════════════════════════════════════════════════════
set -uo pipefail

# ─── Constants ────────────────────────────────────────────────────────────────
DALEC_DIR="$(cd "$(dirname "$0")" && pwd)"
ONBOARD_CLONE="$DALEC_DIR/../aks-dalec-build-defs"
TEST_DIR="specs/containernetworkingauto/test-pr"
BRANCH="ksehgal/fix-publish-poc"
REPO="azure-management-and-platforms/aks-dalec-build-defs"

TMPFILE="$(mktemp)"
BACKUP_ONBOARD="$(mktemp)"
PASS=0
FAIL=0
TEST3_PR_URL=""

# ─── Colours ──────────────────────────────────────────────────────────────────
GREEN='\033[0;32m'
RED='\033[0;31m'
YELLOW='\033[0;33m'
CYAN='\033[0;36m'
NC='\033[0m'

# ─── Helpers ──────────────────────────────────────────────────────────────────

banner() {
  echo ""
  echo -e "${CYAN}════════════════════════════════════════════════════════════${NC}"
  echo -e "${CYAN}  $1${NC}"
  echo -e "${CYAN}════════════════════════════════════════════════════════════${NC}"
}

assert_log() {
  local pattern="$1"
  local description="$2"
  if grep -qE "$pattern" "$TMPFILE"; then
    echo -e "  ${GREEN}✅ PASS${NC} — $description"
    ((PASS++))
  else
    echo -e "  ${RED}❌ FAIL${NC} — $description"
    echo -e "  ${RED}   Expected pattern: $pattern${NC}"
    ((FAIL++))
  fi
}

assert_log_absent() {
  local pattern="$1"
  local description="$2"
  if grep -qE "$pattern" "$TMPFILE"; then
    echo -e "  ${RED}❌ FAIL${NC} — $description"
    echo -e "  ${RED}   Pattern should NOT appear: $pattern${NC}"
    ((FAIL++))
  else
    echo -e "  ${GREEN}✅ PASS${NC} — $description"
    ((PASS++))
  fi
}

extract_pr_number() {
  grep -oE 'Created PR #[0-9]+' "$TMPFILE" | head -1 | grep -oE '[0-9]+'
}

extract_feature_branch() {
  grep -oE 'Created branch [^ ]+' "$TMPFILE" | head -1 | awk '{print $NF}'
}

run_pipeline() {
  cd "$DALEC_DIR"
  echo -e "  ${YELLOW}▶ Running pipeline: go run . -path=containernetworkingauto/test-pr${NC}"
  go run . -path=containernetworkingauto/test-pr 2>&1 | tee "$TMPFILE"
  local pipe_exit=${PIPESTATUS[0]}
  echo ""
  return $pipe_exit
}

# clean_test_dir removes everything in test-pr/ except onboard.yml on the
# remote branch, so the next test starts from a clean first-time state.
clean_test_dir() {
  echo -e "  ${YELLOW}▶ Cleaning $TEST_DIR on $BRANCH …${NC}"
  cd "$ONBOARD_CLONE"
  git checkout "$BRANCH" -- . 2>/dev/null
  git pull origin "$BRANCH" --rebase 2>/dev/null

  # Find files to remove (everything except onboard.yml)
  local to_remove
  to_remove=$(find "$TEST_DIR" -type f ! -name 'onboard.yml' 2>/dev/null || true)

  if [[ -n "$to_remove" ]]; then
    echo "$to_remove" | xargs git rm -f 2>/dev/null || true
    git commit -m "[test] Clean test-pr dir for integration test" 2>/dev/null
    git push origin "$BRANCH" 2>/dev/null
    echo -e "  ${GREEN}✔ Cleaned: removed $(echo "$to_remove" | wc -l | tr -d ' ') file(s)${NC}"
  else
    echo -e "  ${GREEN}✔ Already clean — only onboard.yml present${NC}"
  fi

  cd "$DALEC_DIR"
}

# restore_onboard resets onboard.yml to its original backed-up content.
restore_onboard() {
  echo -e "  ${YELLOW}▶ Restoring original onboard.yml …${NC}"
  cd "$ONBOARD_CLONE"
  cp "$BACKUP_ONBOARD" "$TEST_DIR/onboard.yml"
  git add "$TEST_DIR/onboard.yml"
  if ! git diff --cached --quiet; then
    git commit -m "[test] Restore original onboard.yml"
    git push origin "$BRANCH"
    echo -e "  ${GREEN}✔ onboard.yml restored${NC}"
  else
    echo -e "  ${GREEN}✔ onboard.yml already at original state${NC}"
  fi
  cd "$DALEC_DIR"
}

# ═══════════════════════════════════════════════════════════════════════════════
# Phase 0 — Setup
# ═══════════════════════════════════════════════════════════════════════════════
banner "Phase 0 — Setup"

# Verify prerequisites
if [[ ! -d "$ONBOARD_CLONE/.git" ]]; then
  echo -e "${RED}ERROR: Local clone not found at $ONBOARD_CLONE${NC}" && exit 1
fi

# Backup original onboard.yml
cp "$ONBOARD_CLONE/$TEST_DIR/onboard.yml" "$BACKUP_ONBOARD"
echo "  Backed up onboard.yml → $BACKUP_ONBOARD"

# Ensure onboard.yml has the original tag (v1.6.41) for Test 1
cd "$ONBOARD_CLONE"
git checkout "$BRANCH" -- . 2>/dev/null
git pull origin "$BRANCH" --rebase 2>/dev/null

cat > "$TEST_DIR/onboard.yml" <<'EOF'
repository: https://github.com/Azure/azure-container-networking/tree/master
reviewers: 
  - ryukikoda@microsoft.com
reviewMode: ManualReview
tags: 
  - v1.6.41
dockerfile: cns/Dockerfile
makefile: Makefile
EOF

git add "$TEST_DIR/onboard.yml"
if ! git diff --cached --quiet; then
  git commit -m "[test] Reset onboard.yml to v1.6.41 for test run"
  git push origin "$BRANCH"
fi
cd "$DALEC_DIR"

# Clean test-pr directory (remove specfiles, Dockerfile, Makefile siblings)
clean_test_dir

# ═══════════════════════════════════════════════════════════════════════════════
# Test 1 — First-time Onboard (actionNotify → PR created)
# ═══════════════════════════════════════════════════════════════════════════════
banner "Test 1 — First-time Onboard"
echo "  Precondition: only onboard.yml in test-pr, tags=[v1.6.41], ManualReview"
echo "  Expected: pipeline generates spec, creates PR"
echo ""

run_pipeline
PIPELINE_EXIT=$?

echo -e "\n  ${CYAN}── Assertions ──${NC}"
assert_log "No sibling Dockerfile/Makefile found" "Detected as first-time onboard (no siblings)"
assert_log "(Created PR #|PR created for)" "PR was created"
assert_log_absent "Skipping test-pr" "Did NOT skip"

FEATURE_BRANCH=$(extract_feature_branch)
if [[ -z "$FEATURE_BRANCH" ]]; then
  echo -e "  ${RED}❌ FATAL: Could not extract feature branch from output. Cannot continue.${NC}"
  echo -e "  ${RED}   Aborting remaining tests.${NC}"
  cat "$TMPFILE"
  rm -f "$TMPFILE" "$BACKUP_ONBOARD"
  exit 1
fi
echo -e "  ${GREEN}Feature branch: $FEATURE_BRANCH${NC}"

# ═══════════════════════════════════════════════════════════════════════════════
# Phase 2 — Merge feature branch into base (transition between Test 1 and Test 2)
# ═══════════════════════════════════════════════════════════════════════════════
banner "Phase 2 — Merging $FEATURE_BRANCH into $BRANCH"

cd "$ONBOARD_CLONE"
git fetch origin 2>/dev/null
echo -e "  ${YELLOW}▶ git merge origin/$FEATURE_BRANCH --no-edit${NC}"
if git merge "origin/$FEATURE_BRANCH" --no-edit; then
  git push origin "$BRANCH"
  echo -e "  ${GREEN}✔ Merged $FEATURE_BRANCH into $BRANCH${NC}"
else
  echo -e "  ${RED}❌ FATAL: Failed to merge $FEATURE_BRANCH. Cannot continue.${NC}"
  git merge --abort 2>/dev/null
  rm -f "$TMPFILE" "$BACKUP_ONBOARD"
  exit 1
fi
cd "$DALEC_DIR"

echo -e "  ${YELLOW}Waiting 5s for GitHub to propagate …${NC}"
sleep 5

# ═══════════════════════════════════════════════════════════════════════════════
# Test 2 — Rerun, No New Tag (skip)
# ═══════════════════════════════════════════════════════════════════════════════
banner "Test 2 — Rerun, No New Tag (should skip)"
echo "  Precondition: tags still [v1.6.41], spec+Dockerfile+Makefile now exist on branch"
echo "  Expected: pipeline skips — no actionable tags"
echo ""

run_pipeline
PIPELINE_EXIT=$?

echo -e "\n  ${CYAN}── Assertions ──${NC}"
assert_log "sibling" "Detected existing siblings (re-onboard)"
assert_log "Skipping test-pr.*no actionable tags" "Skipped — no actionable tags"
assert_log_absent "(Created PR #|PR created for)" "No PR was created"

# ═══════════════════════════════════════════════════════════════════════════════
# Test 3 — Tag Bump v1.7.12 (actionBumpCommit or actionNotify)
# ═══════════════════════════════════════════════════════════════════════════════
banner "Test 3 — Tag Bump to v1.6.42"
echo "  Precondition: update onboard.yml tags to [v1.6.41, v1.6.42]"
echo "  Expected: pipeline processes v1.6.42 (bump commit OR new PR if content changed)"
echo ""

# Update onboard.yml with both tags
cd "$ONBOARD_CLONE"
git pull origin "$BRANCH" --rebase 2>/dev/null

cat > "$TEST_DIR/onboard.yml" <<'EOF'
repository: https://github.com/Azure/azure-container-networking/tree/master
reviewers: 
  - ryukikoda@microsoft.com
reviewMode: ManualReview
tags: 
  - v1.6.41
  - v1.6.42
dockerfile: cns/Dockerfile
makefile: Makefile
EOF

git add "$TEST_DIR/onboard.yml"
git commit -m "[test] Add v1.6.42 tag for bump-commit test"
git push origin "$BRANCH"
cd "$DALEC_DIR"

echo -e "  ${YELLOW}Waiting 5s for GitHub to propagate …${NC}"
sleep 5

run_pipeline
PIPELINE_EXIT=$?

echo -e "\n  ${CYAN}── Assertions ──${NC}"
assert_log "sibling" "Detected existing siblings (re-onboard)"
assert_log_absent "Skipping test-pr" "Did NOT skip"

# Test 3 has two valid outcomes depending on whether Dockerfile/Makefile changed
if grep -qE 'Content unchanged|Revision bump pushed' "$TMPFILE"; then
  echo -e "  ${GREEN}✅ PASS${NC} — Route: actionBumpCommit (content unchanged between v1.6.41 → v1.6.42)"
  ((PASS++))
  assert_log "Revision bump pushed" "Revision bump was pushed to remote"
elif grep -qE '(Created PR #|PR created for)' "$TMPFILE"; then
  echo -e "  ${GREEN}✅ PASS${NC} — Route: actionNotify (Dockerfile/Makefile changed between v1.6.41 → v1.6.42)"
  ((PASS++))
  TEST3_PR_URL=$(grep -oE 'Created PR #[0-9]+' "$TMPFILE" | head -1 || true)
  echo -e "  ${YELLOW}  ℹ $TEST3_PR_URL — content changed, so a new PR was created${NC}"
else
  echo -e "  ${RED}❌ FAIL${NC} — Neither bump-commit nor PR creation detected"
  ((FAIL++))
fi

# ═══════════════════════════════════════════════════════════════════════════════
# Phase 5 — Cleanup
# ═══════════════════════════════════════════════════════════════════════════════
banner "Phase 5 — Cleanup"

# Restore original onboard.yml
restore_onboard

# Remove generated files from test-pr/
clean_test_dir

# Cleanup temp files
rm -f "$TMPFILE" "$BACKUP_ONBOARD"

# ═══════════════════════════════════════════════════════════════════════════════
# Summary
# ═══════════════════════════════════════════════════════════════════════════════
banner "Test Summary"
TOTAL=$((PASS + FAIL))
echo -e "  Total:  $TOTAL"
echo -e "  ${GREEN}Passed: $PASS${NC}"
if [[ $FAIL -gt 0 ]]; then
  echo -e "  ${RED}Failed: $FAIL${NC}"
  exit 1
else
  echo -e "  ${GREEN}Failed: 0${NC}"
  echo -e "\n  ${GREEN}All tests passed!${NC}"
  exit 0
fi
