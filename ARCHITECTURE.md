# DALEC Mapping Architecture

## Overview

This document outlines the internal automated workflow for generating and maintaining DALEC spec files from onboarded partner repositories.

## High-Level Flow

```bash
┌─────────────────────────────────────────────────────────────────────────────┐
│                        AUTOMATED DAILY WORKFLOW                             │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│  Every 24 hours, for each onboarded default.yml:                            │
│                                                                             │
│  ┌──────────────┐    ┌──────────────┐    ┌──────────────┐                   │
│  │   Step 1:    │───►│   Step 2:    │───►│   Step 3:    │                   │
│  │   Discover   │    │   Populate   │    │   Generate   │                   │
│  └──────────────┘    └──────────────┘    └──────────────┘                   │
│         │                   │                   │                           │
│         ▼                   ▼                   ▼                           │
│  Validate paths      LLM agent fills     Transform to                       │
│  from default.yml    non-deterministic   final DALEC spec                   │
│                      values                                                 │
│                                                                             │
│  ┌──────────────────────────────────────────────────────────────────┐       │
│  │                    Optional: CVE Patching                        │       │
│  └──────────────────────────────────────────────────────────────────┘       │
│                                                                             │
│                              │                                              │
│                              ▼                                              │
│                   ┌──────────────────┐                                      │
│                   │  Update spec in  │                                      │
│                   │  private branch  │                                      │
│                   └──────────────────┘                                      │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
```

## Input: Onboarded Configuration

Each partner team has a `default.yml` in the private repository:

```yaml
# definitions/<team-name>/default.yml
repository: owner/repo/branch

dockerfiles:
  - Dockerfile
  - docker/Dockerfile.alpine

makefiles:
  - Makefile
  - src/Makefile
```

Location: [azure-management-and-platforms/aks-dalec-build-defs](https://github.com/azure-management-and-platforms/aks-dalec-build-defs)

---

## Step 1: Discover

**Purpose**: Validate that paths specified in `default.yml` exist in the source repository.

### 1.1 Process

1. Read `default.yml` configuration
2. Checkout the source repository at specified branch
3. Verify each dockerfile and makefile path exists
4. Collect file contents for next step

### 1.2 Input

```yaml
repository: owner/repo/branch
dockerfiles:
  - Dockerfile
makefiles:
  - Makefile
```

### 1.3 Output

```yaml
# filepath.yml
dockerfiles:
  - Dockerfile
makefiles:
  - Makefile
```

### 1.4 Error Handling

- If required paths are missing, log warning and proceed with available files
- If repository is unreachable, retry with backoff authorization, then skip this cycle

---

## Step 2: Populate

**Purpose**: Use an LLM agent to fill non-deterministic values in the DALEC spec structure.

### 2.1 Process

1. Load discovered dockerfile and makefile contents
2. Provide the DALEC spec struct template to the agent
3. Agent analyzes build files and populates:
   - Package name and version
   - Build dependencies
   - Runtime dependencies
   - Build commands
   - Environment variables
   - Labels and metadata

### 2.2 Input

```json
{
  "skill": "<SKILL.md content>",
  "parameters": {
    "dockerfiles": ["<content>"],
    "makefiles": ["<content>"],
    "spec_struct": "<DALEC spec template>"
  }
}
```

### 2.3 Output

```yaml
# populated-values.yml
name: package-name
version: 1.2.3
description: "Extracted from Dockerfile"
dependencies:
  build:
    - gcc
    - make
  runtime:
    - libc
build:
  commands:
    - make build
    - make install
```

### Agent Skill

Located at: `generator/skills/non-deterministic-setup/SKILL.md`

---

## Step 3: Generate

**Purpose**: Transform populated values into the final DALEC spec format.

### 3.1 Process

1. Load populated values from Step 2
2. Apply generator transformer logic
3. Validate generated spec against DALEC schema
4. Output final spec file

### 3.2 Transformer

See: `generator/transformer/` for transformation logic

### 3.3 Input

```yaml
# populated-values.yml
name: package-name
version: 1.2.3
# ... populated fields
```

### 3.4 Output

```yaml
# final DALEC spec
name: package-name
version: 1.2.3
targets:
  mariner2:
    image: mcr.microsoft.com/cbl-mariner/base/core:2.0
dependencies:
  build:
    - gcc
    - make
  runtime:
    - libc
build:
  steps:
    - command: make build
    - command: make install
```

---

## Optional: CVE Patching

**Purpose**: Apply security patches for known vulnerabilities.

> ⚠️ Implementation not yet determined

### Potential Process

1. Scan generated spec for known vulnerable dependencies
2. Query CVE database for applicable patches
3. Inject patch steps into build process
4. Update dependency versions where applicable

---

## Final Step: Update Private Repository

**Purpose**: Commit the generated spec to the private branch.

### Target Repository

[azure-management-and-platforms/aks-dalec-build-defs](https://github.com/azure-management-and-platforms/aks-dalec-build-defs)

---

## Scheduling

| Trigger    | Frequency | Description                          |
| ---------- | --------- | ------------------------------------ |
| Scheduled  | Every 24h | Process all onboarded `default.yml`  |
| On-demand  | Manual    | Trigger for specific team/package    |

---

## Component Map

```bash
┌─────────────────────────────────────────────────────────────────────────────┐
│                            COMPONENTS                                       │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│  ┌─────────────────┐                                                        │
│  │  Scheduler      │  Triggers daily workflow                               │
│  └────────┬────────┘                                                        │
│           │                                                                 │
│           ▼                                                                 │
│  ┌─────────────────┐     ┌─────────────────┐     ┌─────────────────┐        │
│  │  tool/          │     │  generator/     │     │  generator/     │        │
│  │  discover.go    │────►│  skills/        │────►│  transformer/   │        │
│  │                 │     │  SKILL.md       │     │                 │        │
│  └─────────────────┘     └─────────────────┘     └─────────────────┘        │
│                                                                             │
│  ┌─────────────────┐                                                        │
│  │  Azure OpenAI   │  LLM for non-deterministic population                  │
│  └─────────────────┘                                                        │
│                                                                             │
│  ┌─────────────────┐                                                        │
│  │  GitHub API     │  Source repo access & spec commit                      │
│  └─────────────────┘                                                        │
│                                                                             │
└─────────────────────────────────────────────────────────────────────────────┘
```

---