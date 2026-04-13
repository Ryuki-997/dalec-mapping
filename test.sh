#!/usr/bin/env bash
# test.sh — Build and smoke-test the generated DALEC spec directly.
#
#   Copies result/specfile.yml → result/output.yml, then runs docker build
#   for each target defined in the spec, matching what step6_testImage does.
#
#   Usage:  ./test.sh [path/to/spec.yml]
#           Defaults to result/specfile.yml
set -uo pipefail

DALEC_DIR="$(cd "$(dirname "$0")" && pwd)"
SPEC="${1:-$DALEC_DIR/result/specfile.yml}"
OUTPUT="$DALEC_DIR/result/output.yml"

GREEN='\033[0;32m'
RED='\033[0;31m'
CYAN='\033[0;36m'
NC='\033[0m'

banner() {
  echo ""
  echo -e "${CYAN}════════════════════════════════════════════════════════════${NC}"
  echo -e "${CYAN}  $1${NC}"
  echo -e "${CYAN}════════════════════════════════════════════════════════════${NC}"
}

ok()   { echo -e "  ${GREEN}✅ $*${NC}"; }
fail() { echo -e "  ${RED}❌ $*${NC}"; exit 1; }

# ─── Preflight ────────────────────────────────────────────────────────────────
banner "Preflight"

[[ -f "$SPEC" ]] || fail "Spec file not found: $SPEC"
ok "Spec: $SPEC"

cp "$SPEC" "$OUTPUT"
ok "Copied to $OUTPUT"

# ─── Build targets ────────────────────────────────────────────────────────────
# Read targets from x-build-extensions.build-targets in the spec.
# Falls back to the two standard targets if none found.
TARGETS=()
while IFS= read -r line; do
  line="${line#"${line%%[![:space:]]*}"}"   # ltrim
  line="${line#- }"
  [[ -n "$line" ]] && TARGETS+=("$line")
done < <(awk '/^x-build-extensions:/,/^[^ ]/' "$SPEC" \
  | grep -A100 'build-targets:' \
  | tail -n +2 \
  | grep '^    - ' \
  | sed 's/^    - //')

if [[ ${#TARGETS[@]} -eq 0 ]]; then
  TARGETS=("azlinux3/container" "windowscross/container")
fi

IMAGE_NAME="$(grep '^name:' "$SPEC" | awk '{print $2}')"
VERSION="$(grep '^  VERSION:' "$SPEC" | awk '{print $2}')"
IMAGE_TAG="${IMAGE_NAME}:${VERSION}"

# ─── BuildKit secret args ─────────────────────────────────────────────────────
# BuildKit's git source resolver honours two well-known secrets:
#   GIT_AUTH_TOKEN  → sent as "Authorization: Bearer <token>"
#   GIT_AUTH_HEADER → sent as "Authorization: <value>"
# ADO accepts Basic auth for both PATs and OAuth tokens, so we compute the
# Basic header from ADO_TOKEN and pass it as GIT_AUTH_HEADER.
export DOCKER_BUILDKIT=1
SECRET_ARGS=()

HAS_ADO_URL=false
if grep -qE 'visualstudio\.com|dev\.azure\.com' "$SPEC"; then
  HAS_ADO_URL=true
fi

if [[ "$HAS_ADO_URL" == true ]]; then
  if [[ -z "${ADO_TOKEN:-}" ]]; then
    echo -e "  ${CYAN}🔑 ADO_TOKEN not set — acquiring OAuth token via az cli...${NC}"
    ADO_TOKEN=$(az account get-access-token --resource 499b84ac-1321-427f-aa17-267ca6975798 --query accessToken -o tsv) \
      || fail "Failed to acquire ADO OAuth token. Run 'az login' first."
    export ADO_TOKEN
    ok "Acquired ADO OAuth token"
  fi
  # Build Basic auth header: base64("":<token>)  — empty username, token as password.
  GIT_AUTH_HEADER="basic $(printf ':%s' "$ADO_TOKEN" | base64)"
  export GIT_AUTH_HEADER
  SECRET_ARGS=(--secret id=GIT_AUTH_HEADER,env=GIT_AUTH_HEADER)
  ok "ADO_TOKEN detected — will pass as BuildKit GIT_AUTH_HEADER secret"
fi

banner "Building ${#TARGETS[@]} target(s) for ${IMAGE_TAG}"

for TARGET in "${TARGETS[@]}"; do
  case "$TARGET" in
    windowscross/*) PLATFORM="windows/amd64" ;;
    *)              PLATFORM="linux/amd64"   ;;
  esac

  SAFE_TARGET="${TARGET//\//-}"
  FULL_TAG="${IMAGE_TAG}-${SAFE_TARGET}"

  echo ""
  echo "  ▶ docker build --platform $PLATFORM -t $FULL_TAG --target $TARGET -f $OUTPUT ."
  if docker build \
      --platform "$PLATFORM" \
      -t "$FULL_TAG" \
      -f "$OUTPUT" \
      --target "$TARGET" \
      ${SECRET_ARGS[@]+"${SECRET_ARGS[@]}"} \
      .; then
    ok "Built $FULL_TAG"
  else
    fail "docker build failed for target $TARGET"
  fi
done

banner "All targets built successfully"
