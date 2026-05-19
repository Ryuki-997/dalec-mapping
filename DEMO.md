# Brownbag: DALEC Spec Generation & Promotion

## 1. Problem Statement

**Context:** Upstream DALEC works with open-source CNCF projects. Our primary concern is different — we need to cover for **AKS-managed images to be FIPS compliant**.

**What partners deal with today:**

- Partner teams need to publish FIPS-compliant container images/packages to MCR and packages.microsoft.com
- Writing DALEC spec files manually is error-prone and requires deep knowledge of the spec format
- No automated way to detect when partners release new upstream versions
- Each new version requires manual spec updates, PR creation, and promotion steps
- Maintaining FIPS compliance across all images requires consistent, reproducible builds

**Who are the partners?** Fleet, Extensions, Azure Policy, Container Networking, Node Controller teams — anyone shipping container images through the AKS managed DALEC pipeline.

---

## 2. Solution Overview

**One sentence:** Partners provide a minimal `onboard.yml`, and the system automatically detects new releases, generates FIPS-compliant DALEC specs, validates builds, and promotes artifacts all the way to MCR/PMC.

**Two onboarding paths:**

| | Path A: Manual Spec | Path B: Auto-Generated |
|---|---|---|
| Partner provides | `onboard.yml` + handwritten specfile | `onboard.yml` only |
| Spec creation | Partner writes it | System generates it |
| Control level | Full granularity | Hands-off |
| Best for | Complex builds, custom requirements | Standard Go/C builds |

Manual spec (Path A) follows the same pipeline — it just skips auto-generation on tag cut. The partner writes the specfile themselves, and everything from validation onward is identical.

**This presentation focuses on Path B (auto-generation).**

**Roadmap:**

1. **CVE patching** — Will support automated patching for any CVEs detected in images (future)
2. **ADO migration** — Will slowly move away from the hybrid GitHub+ADO architecture toward fully ADO-side orchestration

**Current architecture (hybrid):** GitHub handles the user-facing experience (PRs, reviews, status checks). ADO handles the heavy lifting (build, test, sign, publish).

---

## 3. User Flow Walkthrough (Feature Showcase)

> Walk through each stage. Show real PRs / screenshots if possible. No code.

### Stage 1: Onboarding

**What the partner does:**

Creates a single file — `specs/<project>/onboard.yml`:

```yaml
aks-node-controller:
  repository: https://github.com/Azure/aks-node-controller
  tags:
    - "^v0\\.0\\.\\d+$"
  targets:
    - azlinux3/container
  dockerfile: "."
  makefile: "."
  reviewers:
    - user1
```

**That's all they provide.** Repository URL, tag pattern to watch, build targets, and who should review.

Opens a PR on the spec repo → merged.

### Stage 2: Automated Tag Detection (Daily Tag Trigger)

**What happens automatically every day:**

1. System scans all `onboard.yml` files
2. Fetches tags from each partner's upstream repository
3. Matches tags against the regex patterns
4. For new tags not yet processed → triggers spec generation

**What the partner sees:** A PR appears automatically on the spec repo with the generated specfile. No action required from them.

### Stage 3: PR Validation (PR Gateway)

**What happens when a specfile PR exists:**

1. GitHub Action detects `specfile` label on PR
2. Looks up the partner's personalized ADO build pipeline by name
3. Queues the build with the specfile from the PR
4. Polls ADO every 2 minutes for completion
5. Posts pass/fail status back to the GitHub PR

**What the partner sees:** A commit status check (`ADO / dalec-build`) — green checkmark or red X — with a direct link to the ADO build logs.

### Stage 4: Review & Merge

**Reviewer flow:**

- Reviewers from `onboard.yml` are auto-assigned to the PR
- If no reviewers specified → trust mode → auto-merge after validation passes
- 24-hour deduplication prevents duplicate PRs for the same tag

**What the partner sees:** Standard GitHub PR review experience. Approve → merge.

### Stage 5: Promotion Pipeline

**What happens after merge:**

```
PR Merged
    │
    ▼
┌─────────────────────┐
│ Temp Staging        │  ← Build artifacts from validation
└─────────┬───────────┘
          │  Post-Merge Action queues ADO promotion (def 457591)
          ▼
┌─────────────────────┐
│ Staging             │  ← Validated, ready for consolidation
└─────────┬───────────┘
          │  Consolidated Build (daily)
          ▼
┌─────────────────────┐
│ EV2 Artifacts       │  ← Release-ready packages
└─────────┬───────────┘
          │  Consolidated Release
          ▼
┌─────────────────────┐
│ MCR / PMC           │  ← Published to production
└─────────────────────┘
```

**What the partner sees:** Their image shows up in MCR / their package shows up on packages.microsoft.com.

---

## 4. Key Features

| Feature | Description |
|---------|-------------|
| **Smart revision management** | Same version released multiple times → R1, R2, R3 revisions tracked automatically |
| **Multi-repo support** | GitHub (public & private via GitHub Apps) and Azure DevOps repos |
| **10+ build targets** | azlinux3/container, azlinux3/rpm, ubuntu (focal/jammy/noble), debian bookworm, windowscross |
| **Grouped PRs** | Multiple components sharing a tag → single PR (e.g., azure-cni + azure-ipam) |
| **Fast-path bumping** | If only version/commit changed (no Dockerfile changes), skip full regeneration |
| **Per-component test suites** | Optional `tests/test.sh` validates the built image before promotion |
| **Deduplication** | 24-hour window prevents duplicate PRs for the same component+tag |

---

## 5. Architecture Summary

```
┌─────────────────────────────────────────────────────────────────────┐
│                         GITHUB (User-Facing)                        │
│                                                                     │
│  specs repo                    GitHub Actions                       │
│  ├── onboard.yml         ┌─────────────────────────┐               │
│  ├── specfile.yml        │ pr-lifecycle.yml         │               │
│  └── tests/              │  • assign reviewers      │               │
│                          │  • call pr-validation    │               │
│                          │  • promote on approval   │               │
│                          └─────────────────────────┘               │
│                          ┌─────────────────────────┐               │
│                          │ pr-validation.yml        │               │
│                          │  • queue ADO build       │               │
│                          │  • poll every 2 min      │               │
│                          │  • post commit status    │               │
│                          └─────────────────────────┘               │
│                          ┌─────────────────────────┐               │
│                          │ Daily Tag Trigger        │               │
│                          │  • scan onboard files    │               │
│                          │  • detect new tags       │               │
│                          │  • generate specs        │               │
│                          │  • create PRs            │               │
│                          └─────────────────────────┘               │
└──────────────────────────────────┬──────────────────────────────────┘
                                   │ OIDC auth
                                   ▼
┌─────────────────────────────────────────────────────────────────────┐
│                         ADO (Build & Release)                       │
│                                                                     │
│  Personalized Pipeline (per partner)                                │
│    → Build/test specfile, output to temp staging                    │
│                                                                     │
│  Generalized Pipeline (def 457591)                                  │
│    → Move artifacts: temp staging → staging                         │
│                                                                     │
│  Consolidated Build                                                 │
│    → Gather all staging → EV2 packages                              │
│                                                                     │
│  Consolidated Release                                               │
│    → EV2 → MCR / PMC (production)                                   │
└─────────────────────────────────────────────────────────────────────┘
```

**Why hybrid?**
- Partners never leave GitHub — familiar UX, no ADO accounts needed
- Each partner isolated (own ADO pipeline) but consolidated for release
- Frontend (spec generation) evolves independently of backend (ADO builds)
- Aligned with upstream `dalec-build-defs` patterns

---

## 6. Hand Off to Backend

> Transition point. Suggested topics for backend team:
> - ADO pipeline internals (personalized build, signing, staging)
> - Consolidated build/release flow
> - EV2 deployment details
> - ESRP signing and ACR publishing
> - How to set up a new personalized pipeline for a partner

---

## Visual Aids Checklist

- [ ] Real `onboard.yml` example (use aks-node-controller or azure-cni)
- [ ] Screenshot of auto-created PR with generated specfile
- [ ] Screenshot of PR status check (`ADO / dalec-build` green/red)
- [ ] Screenshot of reviewer auto-assignment
- [ ] Promotion flow diagram (above)
- [ ] Architecture diagram (above)
