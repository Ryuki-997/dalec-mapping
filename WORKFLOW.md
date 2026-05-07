# Pipeline Flow

## Overview

This document describes the end-to-end pipeline behavior from the moment a partner team submits a pull request through to image publication on MCR/PMC. Use this to understand what triggers each stage and how to monitor your image's progress through the system.

```bash
┌──────────────────────────────────────────────────────────────────────────────────────────┐
│                              END-TO-END PIPELINE FLOW                                    │
├──────────────────────────────────────────────────────────────────────────────────────────┤
│                                                                                          │
│  Partner submits PR                                                                      │
│        │                                                                                 │
│        ▼                                                                                 │
│  ┌──────────────────┐     direct call         ┌──────────────────────────┐               │
│  │  #1 PR Gateway   │ ─────────────────────►  │  ADO: Personalized       │               │
│  │ (GitHub Actions) │  (by partber NAME)      │  Build/Test Pipeline     │               │
│  └──────────────────┘                         └────────────┬─────────────┘               │
│        │                                                   │                             │
│        ▼                                                   ▼                             │
│  PR status check ◄──────────────────────────────── pass/fail result                      │
│        │                                                                                 │
│        ▼                                                                                 │
│  Manual/Auto review                                                                      │
│        │                                                                                 │
│        ▼  (merge)                                                                        │
│  ┌──────────────────┐     direct call         ┌──────────────────────────┐               │
│  │  #2 Post-Merge   │ ─────────────────────►  │  ADO: Generalized        │               │
│  │ (GitHub Actions) │      (by DEF ID)        │  Pipeline (pre→staging)  │               │
│  └──────────────────┘                         └────────────┬─────────────┘               │
│                                                            │                             │
│                                                            ▼                             │
│                                               ┌──────────────────────────┐               │
│                                               │  ADO: Consolidated Build │               │
│                                               │  (staging → EV2)         │               │
│                                               └────────────┬─────────────┘               │
│                                                            │                             │
│                                                            ▼                             │
│                                               ┌──────────────────────────┐               │
│                                               │  ADO: Consolidated       │               │
│                                               │  Release (EV2 → MCR/PMC) │               │
│                                               └──────────────────────────┘               │
│                                                                                          │
│  ┌──────────────────┐                                                                    │
│  │  #3 Daily Tag    │  Runs daily, creates PRs for new tags                              │
│  │ (GitHub Actions) │  (feeds back into #1 PR Gateway above)                             │
│  └──────────────────┘                                                                    │
│                                                                                          │
└──────────────────────────────────────────────────────────────────────────────────────────┘
```

---

## One-Time Setup

Before a partner's repository can flow through the system, the following infrastructure is created manually per customer:

1. **Personalized ADO Build/Test Pipeline** — A dedicated build/test pipeline parameterized for the partner's spec file and branch. One is created per partner, named `[build][aks-managed-dalec] {spec_repo}`.
2. **Generalized Promotion Pipeline** — A single shared pipeline (definition ID 457591) that promotes any partner's artifacts from temporary staging to staging.

The GitHub Actions workflows call the personalized ADO pipeline by partner name (PR Gateway) and the generalized pipeline by definition ID (Post-Merge). Each new partner requires manual creation of their personalized pipeline before their PRs can be validated.

> **Note:** This setup is currently performed manually by the DALEC team. Automation is planned for the future.

---

## Onboarding Paths

Partners choose one of two paths depending on the level of control they need.

### Path A: Manual Spec File

The partner writes their own DALEC spec file following the [upstream standard template](https://github.com/Azure/dalec-build-defs/blob/main/template.yml). This path offers **more control and granularity** over the build definition.

```bash
┌──────────────────────────────────────────────────────────────────┐
│  1. Partner forks the spec repo                                  │
│  2. Creates specs/<project>/ folder containing:                  │
│     ├── onboard.yaml                                             │
│     ├── <component>-specfile.yml                                 │
│     ├── Makefile                                                 │
│     └── Dockerfile                                               │
│  3. Opens pull request against spec repo                         │
│  4. PR Gateway (#1) triggers personalized ADO pipeline           │
│  5. Review (manual or auto) on the PR                            │
│  6. PR merged → Post-Merge (#2) promotes to staging              │
└──────────────────────────────────────────────────────────────────┘
```


### Path B: Auto Spec File 

The partner submits only an `onboard.yaml` with basic information (repository URL, tag patterns, targets). The system **auto-generates the full specfile** on the partner's behalf.

```bash
┌──────────────────────────────────────────────────────────────────┐
│  1. Partner forks the spec repo                                  │
│  2. Creates specs/<project>/ folder containing:                  │
│     └── onboard.yaml  (only)                                     │
│  3. Opens pull request against spec repo                         │
│  4. PR Gateway (#1) passes immediately (no specfile to test)     │
│  5. PR is merged                                                 │
│  6. Daily Tag Trigger (#3) runs next day:                        │
│     - Detects new onboard.yaml with no existing spec files       │
│     - Generates the first specfile automatically                 │
│     - Creates a new PR with the generated specfile               │
│  7. Generated PR flows through normal Path A from step 4 onward  │
└──────────────────────────────────────────────────────────────────┘
```

> ⚠️ **Caution:** If no `reviewers` are specified in `onboard.yaml`, the system treats this as **full trust** — generated PRs will be auto-merged without human review. Partners should only omit reviewers if they are confident in the automated generation and testing pipeline. When in doubt, always specify reviewers.

---

## GitHub Actions Pipelines

### Pipeline #1: PR Gateway

| Field | Value |
|-------|-------|
| **Trigger** | Pull request opened/updated on spec repo |
| **Purpose** | Validate the specfile by triggering a personalized build/test |
| **Mechanism** | Calls the partner's personalized ADO pipeline directly by name |
| **Parameters passed** | Specfile path within the PR, branch the PR is on |
| **Result** | ADO pipeline pass/fail is reported back as a PR status check |
| **Limitation** | Only works if the partner's tests are not tied to a service connection |

### Pipeline #2: Post-Merge

| Field | Value |
|-------|-------|
| **Trigger** | Pull request merged into spec repo |
| **Purpose** | Promote validated artifacts from temporary staging to normal staging |
| **Mechanism** | Calls the generalized ADO promotion pipeline by definition ID (457591) |
| **Result** | Artifacts moved from temporary staging storage account to staging storage account |

### Pipeline #3: Daily Tag Trigger (Frontend Flow)

| Field | Value |
|-------|-------|
| **Trigger** | Scheduled daily |
| **Purpose** | Detect new upstream tags and create PRs for spec file generation/updates |
| **Mechanism** | Runs the frontend flow (this Go codebase) for all onboarded customers |
| **Result** | Creates pull requests when new matching tags are found |
| **Interaction** | Created PRs feed back into Pipeline #1 (PR Gateway) for validation |

**Tag update review modes:**

- **Manual review** — PR is created, reviewers are notified. Must still pass PR Gateway (#1) before merge.
- **Automatic review** — PR passes PR Gateway (#1), then is merged automatically without human intervention.

---

## ADO Pipelines

### 1. Personalized Build/Test Pipeline

| Field | Value |
|-------|-------|
| **Trigger** | Direct call from GitHub Actions PR Gateway (#1) |
| **Parameters** | Specfile path, branch name (the PR branch) |
| **Behavior** | Checks out the PR branch, grabs the specfile from the parameter path, runs build and test |
| **Output** | Build artifacts placed in temporary staging storage account |
| **Note** | Currently tied to a single branch per partner (e.g., `ksehgal/publishXYZ`). The branch parameter allows it to check out the correct PR branch. |

### 2. Generalized Pipeline (Temporary Staging → Staging)

| Field | Value |
|-------|-------|
| **Trigger** | Direct call from GitHub Actions Post-Merge (#2) |
| **Behavior** | Moves validated build artifacts from the temporary staging storage account to the normal staging storage account |
| **Output** | Artifacts available in staging for consolidated build |

### 3. Consolidated Build (Staging → EV2 Artifacts)

| Field | Value |
|-------|-------|
| **Trigger** | Runs after staging is updated (all partners' artifacts consolidated) |
| **Behavior** | Gathers all staged artifacts and assembles them into EV2-compatible deployment packages |
| **Output** | EV2 artifacts ready for release |

### 4. Consolidated Release (EV2 → MCR/PMC)

| Field | Value |
|-------|-------|
| **Trigger** | Runs after consolidated build completes |
| **Behavior** | Deploys EV2 artifacts through the release pipeline to publish images/packages |
| **Output** | Images published to MCR (Microsoft Container Registry) and/or PMC (packages.microsoft.com) |

---

## Artifact Flow

```bash
┌──────────────┐       ┌──────────────┐       ┌──────────┐       ┌──────────┐
│  Temporary   │       │   Staging    │       │   EV2    │       │ MCR/PMC  │
│  Staging     │──────►│   Storage    │──────►│ Artifacts│──────►│ Published│
│  (per-PR)    │       │   Account    │       │          │       │          │
└──────────────┘       └──────────────┘       └──────────┘       └──────────┘
     ▲                        ▲                     ▲                  ▲
     │                        │                     │                  │
  Personalized            Generalized          Consolidated       Consolidated
  Build/Test              Pipeline             Build              Release
  (ADO #1)               (ADO #2)             (ADO #3)           (ADO #4)
```

---

## Future Tag Updates (Steady State)

Once a partner is onboarded, the system monitors for new upstream tags automatically:

1. **Daily Tag Trigger (#3)** scans all `onboard.yaml` files for tag patterns.
2. When a new matching tag is detected in the partner's upstream repository:
   - A specfile is generated or bumped (see [ARCHITECTURE.md](ARCHITECTURE.md) for details).
   - A pull request is created on the spec repo.
3. The created PR enters the normal validation flow:
   - **PR Gateway (#1)** triggers the personalized ADO build/test.
   - Depending on review mode (manual or automatic), the PR is either held for reviewers or auto-merged on success.
4. On merge, **Post-Merge (#2)** promotes artifacts to staging.
5. **Consolidated Build** and **Consolidated Release** publish to MCR/PMC.

---

## Monitoring Checklist

| What to check | Where to look |
|----------------|---------------|
| PR validation status | GitHub PR status checks on the spec repo |
| Build/test logs | ADO personalized pipeline run (linked from PR status) |
| Staging promotion | ADO generalized pipeline run |
| New tag detection | Daily Tag Trigger (#3) workflow runs in GitHub Actions |
| Final publication | Consolidated Release pipeline in ADO |

---

## Architecture Trade-offs: Hybrid (GitHub + ADO) vs Fully ADO

The current system splits orchestration across two platforms: **GitHub Actions** handles PR lifecycle, tag monitoring, polling, and status reporting, while **Azure DevOps** handles build execution, artifact staging, and release. To others, this can appear redundant — GitHub Actions is essentially queuing ADO builds and then sitting idle polling for results.

This section compares the current hybrid approach against a fully-ADO alternative where all orchestration, build, test, and publish logic lives in ADO pipelines.

### What Each Platform Does Today

| Responsibility | GitHub | Azure DevOps |
|----------------|--------|--------------|
| Spec file storage & version control | `specs/` directory in GitHub repo | — |
| PR validation trigger | PR Gateway (`pr-validation.yml`) | — |
| Build & test execution | — | Personalized Build/Test Pipeline |
| Poll build status | `poll-ado` job (every 2 min, up to 24h) | — |
| Post PR commit status | `post_status` in `pr-validation.yml` | — |
| Reviewer assignment | `pr-lifecycle.yml` reads `onboard.yml` | — |
| Branch cleanup | `pr-lifecycle.yml` on PR close | — |
| Post-merge promotion trigger | `pr-lifecycle.yml` queues ADO pipeline 457591 | Generalized Pipeline moves artifacts |
| Daily tag detection | Daily Tag Trigger (#3) via `cmd/spec-generation/` | — |
| Spec generation | `cmd/spec-generation/` Go binary | — |
| Artifact build & sign | — | `azcu` CLI, ESRP signing, ACR publish |
| Consolidated build & release | — | Consolidated Build → EV2 → MCR/PMC |

### Option A: GitHub Polls + ADO Build/Test/Publish (Current)

#### Pros

1. **Partner-facing workflow is GitHub-native** — Partners interact exclusively through GitHub: fork the spec repo, open a PR, and receive review feedback. They do not need ADO project access, ADO accounts, or familiarity with ADO pipeline structure. This is the interface partners already use.

2. **Clear per-image build visibility on PRs** — Each PR's commit status links directly to the specific ADO pipeline run for that image. The status context (`ADO / dalec-build`) makes it immediately clear which pipeline ran and whether it passed, with a direct link to the ADO build logs.

3. **GitHub review ecosystem** — Reviewer assignment from `onboard.yml`, branch protection rules, auto-merge, label-gated triggers (`specfile` label), and branch cleanup all use GitHub-native features without reimplementation.

4. **Independent evolution** — The frontend flow (tag detection, spec generation in `cmd/spec-generation/`) evolves independently of ADO build infrastructure. Changes to how specs are generated don't require ADO pipeline modifications and vice versa.

5. **Individual pipeline per partner is intentional** — Each partner gets a dedicated personalized ADO build/test pipeline named `[build][aks-managed-dalec] {spec_repo}`. This isolation ensures partner builds don't interfere with each other and provides clear ownership and audit trail per image.

6. **Aligned with upstream** — The upstream `dalec-build-defs` repo uses the same hybrid model (GitHub for specs and PR workflow, ADO for build/publish). Staying on this pattern minimizes divergence from upstream, making it easier to pull upstream changes and share tooling.

#### Cons

1. **Multi-platform overhead** — Orchestration is split across GitHub Actions and ADO when it could be centralized in a single platform. Maintaining workflows in both systems adds operational surface area: two sets of logs to check, two sets of permissions to manage, and two CI/CD systems to keep updated.

2. **Bidirectional authentication** — GitHub Actions must authenticate into ADO to queue builds and poll status, then authenticate back into GitHub to post commit statuses and manage PRs. This requires maintaining OIDC federation (GitHub → ADO) and GitHub tokens (for API writes) simultaneously. In contrast, a fully-ADO system only needs to pull information from GitHub in one direction.

### Option B: Fully ADO (All-in-ADO)

#### Pros

1. **Single orchestration platform** — All trigger, build, test, publish, and status reporting logic lives in ADO. No cross-platform polling, no status bridging, no dual authentication. All build, test, and publish logs are in one ADO project. Tracing failures in a single system without cross-referencing GitHub Actions run logs.

2. **No polling waste** — ADO pipelines natively track their own execution. Completion triggers downstream stages without external polling. No self-hosted runner sits idle waiting.

3. **Simplified auth** — Single auth domain (Entra ID / AAD for ADO). No OIDC federation between GitHub and Azure, no `GITHUB_TOKEN` → ADO token exchange.

#### Cons

1. **Migration cost** — Rewriting `pr-validation.yml`, `pr-lifecycle.yml`, and the daily tag trigger as ADO pipelines. Recreating reviewer assignment logic, branch cleanup, label-based gating, and the spec-generation Go binary's GitHub integration points.

2. **ADO PR experience** — ADO pull request UX is less familiar to most external/open-source partner teams compared to GitHub. Review workflows, comment threading, and status check presentation differ significantly.

3. **Upstream divergence risk** — Further customizing ADO pipeline structure beyond what upstream `dalec-build-defs` provides increases the maintenance burden when pulling upstream changes.

### Summary Comparison

| Dimension | Hybrid (Current) | Fully ADO |
|-----------|------------------|-----------|
| Partner onboarding friction | Low (GitHub only) | Higher (ADO access needed) |
| Per-image build visibility | Clear (PR status links to specific ADO run) | Native (ADO dashboard) |
| Authentication | Bidirectional (GitHub ↔ ADO) | Unidirectional (ADO → GitHub) |
| Orchestration surface area | Two platforms to maintain | Single platform |
| PR/review workflow | GitHub-native | Requires reimplementation |
| Upstream alignment | Matches upstream pattern | Diverges from upstream |
| Frontend/backend independence | Decoupled (spec-gen evolves independently) | Coupled |
| Migration effort | N/A (current state) | Significant |
