# DALEC Mapping Architecture

## Overview

This document outlines the automated workflow for generating and maintaining DALEC spec files from onboarded partner repositories.

## High-Level Flow

``` bash
┌──────────────────────────────────────────────────────────────────────────────┐
│                         AUTOMATED DAILY WORKFLOW                             │
├──────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│  Every 24 hours, scan specs/ folder for onboard.yml files:                   │
│                                                                              │
│  ┌────────────┐   ┌────────────┐   ┌────────────┐   ┌────────────────────┐   │
│  │  Step 1:   │──►│  Step 2:   │──►│  Step 3:   │──►│     Step 4:        │   │
│  │  Fetch     │   │  Resolve   │   │  Discover  │   │  Take Action       │   │
│  │  Onboard   │   │  Tag Cache │   │  Build     │   │                    │   │
│  └────────────┘   └────────────┘   │  Files     │   │  ┌──────────────┐  │   │
│        │                │          └────────────┘   │  │  Generate    │  │   │
│        ▼                ▼                │          │  │  Full Spec   │  │   │
│  Scan specs/ for   Expand to            ▼           │  └──────┬───────┘  │   │
│  onboard.yml and   (component,tag)  Fetch fresh     │         │          │   │
│  flatten grouped   pairs with       build files     │         OR         │   │
│  components into   commit SHAs      and compare     │         │          │   │
│  state list                         to cached       │  ┌──────┴───────┐  │   │
│                                                     │  │  Bump Commit │  │   │
│                                                     │  │  (fast path) │  │   │
│                                                     │  └──────────────┘  │   │
│                                                     └────────┬───────────┘   │
│                                                             │                │
│                                                             ▼                │
│                                                    ┌────────────────────┐    │
│                                                    │     Step 5:        │    │
│                                                    │     Create PR      │    │
│                                                    └────────────────────┘    │
│                                                                              │
└──────────────────────────────────────────────────────────────────────────────┘
```

---

## Input: Onboard Configuration

Each partner team has an `onboard.yml` in the spec repository under `specs/<partnership>/onboard.yml`. The file contains **standalone** components (single entry) and **grouped** components (two or more entries that share a PR).

```yaml
# specs/<partnership>/onboard.yml
standalone-component:
  repository: https://github.com/owner/repo
  tags:
    - "v1.2.*"
  targets:
    - azlinux3
  reviewers:
    - user1
  dockerfile: "."
  makefile: "."

group-name:
  component-a:
    repository: https://github.com/owner/repo-a
    tags:
      - "v2.*.*"
    targets:
      - azlinux3
      - windowscross
    reviewers:
      - user2
    dockerfile: "docker/"
    makefile: "."
  component-b:
    repository: https://github.com/owner/repo-a
    tags:
      - "v2.*.*"
    targets:
      - azlinux3
      - windowscross
    reviewers:
      - user2
    dockerfile: "docker/"
    makefile: "."
```

Location: [azure-management-and-platforms/aks-dalec-build-defs](https://github.com/azure-management-and-platforms/aks-dalec-build-defs)

---

## Step 1: Fetch Onboard

**Purpose**: Scan the `specs/` folder for all `onboard.yml` leaf nodes and flatten their components into a state list.

### Process

1. Recursively fetch the spec repository tree under the target input path (e.g. `specs/`)
2. Identify entries where the leaf node is `onboard.yml`
3. For each `onboard.yml`, parse standalone and grouped components
4. Flatten all components into individual `pipeline.State` entries (one per component)
5. Track existing file paths in the spec repo for later revision calculation

### Output

- `[]pipeline.State` — One entry per component, with `Onboard` (ComponentConfig) populated
- `map[string]bool` — Lookup of existing spec file paths in the repo

---

## Step 2: Resolve Tag Cache

**Purpose**: Expand the component-level state list into (component, tag) pairs by resolving tag patterns against repository tags. Centralizes all components with potential changes as key-value pairs of tag name to commit SHA.

### Process

1. For each component state, fetch all tags from the source repository
2. Cache tags globally: `TagCache[repoURL][tagName] = commitSHA`
3. Match each component's `tag_patterns` against the fetched tags
4. Filter out tags that already have a spec file in the remote repo
5. Expand each matched tag into a new state with a populated `TagSet`

### TagSet

```go
type TagSet struct {
    Pattern   string  // Original regex pattern (e.g. "v1.2.*")
    Full      string  // Resolved tag from repo (e.g. "azure-ipam/v1.2.3")
    Stripped  string  // Short semver with v prefix (e.g. "v1.2.3")
    Version   string  // Numeric only (e.g. "1.2.3")
    Revision  int     // Spec revision number (1, 2, 3...)
}
```

### Output

- `[]pipeline.State` — Expanded to one entry per (component, tag) pair, with `Onboard` and `Tag` populated

---

## Step 3: Discover Build Files

**Purpose**: Fetch fresh Dockerfile/Makefile from the source repository at the resolved tag and compare against cached versions to determine what type of action the component needs.

### Process

1. Load cached build files (previously committed Dockerfile/Makefile from the onboard repo)
2. Fetch fresh build files from the source repo at the resolved tag
3. Diff fresh content against cached content
4. Return `contentChanged` flag indicating whether files differ

### Decision Matrix

| Condition | Action |
| --------- | ------ |
| First onboard (no cached files exist) | Generate full spec |
| Content changed (Dockerfile/Makefile differ) | Generate full spec |
| Content unchanged AND prior revision exists | Bump commit (fast path) |
| Content unchanged AND no prior revision | Generate full spec |

### Output

- `bool` — `contentChanged` flag
- Side effect: updates `pipeline.Current.Onboard.DockerfileContent` and `MakefileContent` with fresh content

---

## Step 4: Take Action

**Purpose**: Based on the discovery result, take the corresponding action to produce a DALEC spec file.

### Path A: Generate Full Spec

When Dockerfile/Makefile content has changed or this is a first onboard:

1. Fetch repository metadata (description, license, default branch)
2. Parse Dockerfile into structured AST (stages, args, runs, copies, etc.)
3. Parse Makefile into extracted variables
4. Transform parsed data into a DALEC spec using the transformer pipeline:
   - Extract targets, defaults, sources, dependencies
   - Extract build commands, symlinks, artifacts, tests
5. Write the final spec YAML to the result directory

### Path B: Bump Commit (Fast Path)

When Dockerfile/Makefile content is unchanged and a prior revision exists:

1. Fetch the previous revision's spec file from the remote repo
2. Update `args.COMMIT` with the new tag's commit SHA (from TagCache)
3. Update `args.VERSION` with the new tag version
4. Write the updated spec YAML to the result directory

### Output

- Spec file written to `result/output.yml`
- Remote path for the spec (e.g. `specs/<partnership>/<image>/<image>-<tag>-<revision>-specfile.yml`)

---

## Step 5: Create PR

**Purpose**: Commit all generated/bumped spec files and open a pull request with reviewers.

### Process

1. Group processed components by their group name (or standalone)
2. Generate a unique PR ID (`YYYYMMDD-xxxxxx`)
3. Create a feature branch from the onboard branch
4. Collect all files for the commit (spec files + sibling Dockerfiles/Makefiles)
5. Create a single commit with all files on the feature branch
6. Open a pull request with a descriptive title and body
7. Add configured reviewers to the PR

### Feature Branch Format

```bash
dalec/<specRepository>/<componentName>/<version>-R<revision>/<prID>
```

---

## Scheduling

| Trigger   | Frequency | Description                        |
| --------- | --------- | ---------------------------------- |
| Scheduled | Every 24h | Process all onboarded `onboard.yml` |
| On-demand | Manual    | Trigger for specific partnership   |

---

## Component Map

```bash
┌──────────────────────────────────────────────────────────────────────────────┐
│                              COMPONENTS                                       │
├──────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│  ┌──────────────────┐                                                        │
│  │  Scheduler       │  Triggers daily workflow                               │
│  └────────┬─────────┘                                                        │
│           │                                                                  │
│           ▼                                                                  │
│  ┌──────────────────┐                                                        │
│  │  workflow/       │  Pipeline orchestration (Steps 1-5)                    │
│  └────────┬─────────┘                                                        │
│           │                                                                  │
│           ├──────────────────────────────────────────────────┐               │
│           │                                                  │               │
│           ▼                                                  ▼               │
│  ┌──────────────────┐     ┌──────────────────┐     ┌─────────────────┐       │
│  │  infrastructure/ │     │  infrastructure/ │     │  infrastructure/│       │
│  │  parser/         │     │  transformer/    │     │  repository/    │       │
│  │                  │     │                  │     │                 │       │
│  │  - Dockerfile    │────►│  - Targets       │     │  - GitHub API   │       │
│  │  - Makefile      │     │  - Dependencies  │     │  - ADO API      │       │
│  │  - Spec Writer   │     │  - Sources       │     │                 │       │
│  └──────────────────┘     │  - Build Steps   │     └─────────────────┘       │
│                           │  - Artifacts     │                               │
│                           └──────────────────┘                               │
│                                                                              │
│  ┌──────────────────┐                                                        │
│  │  pipeline/       │  Centralized state + tag cache                         │
│  └──────────────────┘                                                        │
│                                                                              │
└──────────────────────────────────────────────────────────────────────────────┘
```
