---
name: Dalec Spec Generator
description: Agent skill for generating Dalec specification files from GitHub repositories with deterministic workflow
---

# Dalec Spec Generator - Agent Skill

This tool implements a deterministic agent skill for converting GitHub repositories into Dalec specification files for container and package builds.

---

## Workflow Overview

The dalec-spec-generator follows this sequence:

```bash
┌───────────────────────────────────────────────────────────────────┐
│  Step 0: Discover Build Files (--discover flag)                   │
│  ──────────────────────────────────────────────────────────────── │
│  Command: go run main.go -repo owner/repo --discover              │
│  Action: DFS search repository for Dockerfile and Makefile paths  │
│  Output: result/{repo}/filepath.yml                               │
│  Note: Clears result directory before starting fresh              │
└───────────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌─────────────────────────────────────────────────────────────────┐
│  Step 1: Download Build Files & Extract Values                  │
│  ─────────────────────────────────────────────────────────────  │
│  Trigger: filepath.yml contains Dockerfile and/or Makefile paths│
│  Action: Download files from paths, extract non-deterministic   │
│          values using .github/skills/non-deterministic-setup    │
│  Output: result/{repo}/Dockerfile                               │
│          result/{repo}/Makefile                                 │
│          result/{repo}/NonDeterministicValues.yml               │
└─────────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌─────────────────────────────────────────────────────────────────┐
│  Step 2: Run CLI Tool to Generate Dalec Spec                    │
│  ─────────────────────────────────────────────────────────────  │
│  Command: go run main.go -repo <repo> \                         │
│           -dockerfile result/{repo}/Dockerfile \                │
│           -makefile result/{repo}/Makefile \                    │
│           -output result/{repo}/{name}.yml                      │
│  Output: result/{repo}/{name}.yml                               │
└─────────────────────────────────────────────────────────────────┘
```

---

## Directory Structure

All output is placed in the `result/` directory:

```bash
dalec-mapping/
├── result/
│   └── {repo}/
│       └── {subdir}/              # Only if subdirectory specified
│           ├── filepath.yml       # Discovered file paths (from --discover)
│           ├── Dockerfile         # Downloaded from GitHub
│           ├── Makefile           # Downloaded from GitHub
│           ├── NonDeterministicValues.yml # Agent-extracted values
│           └── {name}.yml         # Generated Dalec spec
└── ...
```

### Directory Naming Convention

| Input Format | Directory Created |
| ------------ | ----------------- |
| `owner/repo` | `result/{repo}/` |
| `owner/repo/tree/branch` | `result/{repo}/` |
| `owner/repo/tree/branch/subdir` | `result/{repo}/{subdir}/` |

**Examples:**

- `kubernetes/autoscaler` → `result/autoscaler/`
- `kubernetes/autoscaler/tree/master/addon-resizer` → `result/autoscaler/addon-resizer/`

---

## Detailed Workflow Steps

### Step 0: Discover Build Files

**CRITICAL:** Always run `--discover` first to find all Dockerfiles and Makefiles in the repository.

**Command:**

```bash
go run main.go -repo owner/repo --discover
# Or with branch:
go run main.go -repo owner/repo/tree/main --discover
```

**Action:** DFS search the repository for all Dockerfiles and Makefiles

- **Input:** Repository URL or `owner/repo` format
- **Operations:**
  1. Clear the `result/{repo}/` directory (fresh start each run)
  2. Parse repository URL to extract: owner, repo, branch
  3. DFS traverse repository via GitHub API
  4. Find all files named `Dockerfile`, `*.dockerfile`, or `Makefile`
  5. Skip directories: vendor, node_modules, .git, test, tests, docs, examples
  6. Write discovered paths to `result/{repo}/filepath.yml`
- **Output:** `result/{repo}/filepath.yml`

**filepath.yml format:**

```yaml
dockerfiles:
  - deploy/Dockerfile
  - pkg/blobplugin/Dockerfile
  - cmd/Dockerfile
makefiles:
  - Makefile
  - build/Makefile
```

**Validation:**
- Command exits with status 0
- filepath.yml exists in result directory
- At least one Dockerfile or Makefile found

---

### Step 1: Download Files & Extract Non-Deterministic Values

**Trigger:** filepath.yml exists and contains file paths

**Action:** 
1. Read `result/{repo}/filepath.yml` 
2. Select the appropriate Dockerfile and Makefile from the paths
3. Download the selected files to `result/{repo}/`
4. Extract non-deterministic values per [non-deterministic-setup](../non-deterministic-setup/SKILL.md)
5. Write `result/{repo}/NonDeterministicValues.yml`

**Selection Heuristics (for agent):**
- **Dockerfile priority:** 
  1. Root-level `Dockerfile` 
  2. `deploy/Dockerfile`
  3. `pkg/*/Dockerfile` (matches main package)
  4. First in list
- **Makefile priority:**
  1. Root-level `Makefile`
  2. First in list

**Download URLs:**
```
https://raw.githubusercontent.com/{owner}/{repo}/{branch}/{path}
```

**Output Files:**
- `result/{repo}/Dockerfile`
- `result/{repo}/Makefile`  
- `result/{repo}/NonDeterministicValues.yml`

---

### Step 2: Run CLI Tool to Generate Dalec Spec

**CRITICAL:** This step uses the CLI tool. The agent MUST use the CLI.

**Command:**

```bash
go run main.go \
  -repo owner/repo/tree/branch \
  -dockerfile result/{repo}/Dockerfile \
  -makefile result/{repo}/Makefile \
  -output result/{repo}/{name}.yml
```

**Operations:**
- CLI parses Dockerfile and Makefile
- CLI fetches GitHub metadata (version, commit, license)
- CLI reads NonDeterministicValues.yml from same directory as Dockerfile
- CLI generates complete Dalec spec
- CLI writes output YAML file

**Output:** `result/{repo}/{name}.yml`

**Validation:**
- Command exits with status 0
- Output YAML file exists and is valid YAML

---

## Agent Execution Summary

The agent performs **3 steps**:

1. **Step 0:** Run `go run main.go -repo X --discover` to find all build files
2. **Step 1:** Read filepath.yml, download correct files, create NonDeterministicValues.yml
3. **Step 2:** Run `go run main.go -repo X -dockerfile Y -makefile Z -output O` to generate spec

---

## CLI Reference

### Discover Mode

```bash
go run main.go -repo <repository> --discover
```

**Parameters:**
- `-repo`: GitHub repository (required)
  - Formats: `owner/repo`, `owner/repo/tree/branch`, `owner/repo/tree/branch/subdir`
- `--discover`: Enable discovery mode (no value needed)

**Output:**
- Clears `result/{repo}/` directory
- Creates `result/{repo}/filepath.yml` with discovered paths

### Generate Mode (default)

```bash
go run main.go -repo <repository> -dockerfile <path> -makefile <path> -output <file>
```

**Parameters:**
- `-repo`: GitHub repository (required)
- `-dockerfile`: Path to local Dockerfile
- `-makefile`: Path to local Makefile
- `-output`: Output YAML file path

---

## Example Usage

### Simple Repository

```bash
# Step 0: Discover
go run main.go -repo google/cadvisor --discover

# Review filepath.yml
cat result/cadvisor/filepath.yml

# Step 1: Download files (agent does this manually via curl)
curl -s "https://raw.githubusercontent.com/google/cadvisor/master/deploy/Dockerfile" \
  -o result/cadvisor/Dockerfile
curl -s "https://raw.githubusercontent.com/google/cadvisor/master/Makefile" \
  -o result/cadvisor/Makefile

# Step 1: Create NonDeterministicValues.yml (agent extracts values)

# Step 2: Generate
go run main.go -repo google/cadvisor \
  -dockerfile result/cadvisor/Dockerfile \
  -makefile result/cadvisor/Makefile \
  -output result/cadvisor/cadvisor.yml
```

### Monorepo with Subdirectory

```bash
# Step 0: Discover (searches entire repo)
go run main.go -repo kubernetes/autoscaler/tree/master/addon-resizer --discover

# Review filepath.yml
cat result/autoscaler/addon-resizer/filepath.yml

# Step 1 & 2: Download and generate
# ... (same pattern as above)
```

---

## Error Handling

### Non-Fatal Warnings

- ⚠️ No Dockerfile found in repository
- ⚠️ No Makefile found in repository
- ⚠️ Multiple Dockerfiles found - agent should select appropriate one

### Fatal Errors

- ❌ Repository not found
- ❌ GitHub API rate limit exceeded
- ❌ No build files discovered

---

## Success Criteria

A successful run produces:

1. ✅ `result/{repo}/filepath.yml` with discovered paths
2. ✅ `result/{repo}/Dockerfile` downloaded
3. ✅ `result/{repo}/Makefile` downloaded (if exists)
4. ✅ `result/{repo}/NonDeterministicValues.yml` created
5. ✅ `result/{repo}/{name}.yml` generated

---

## Deterministic Behavior

This agent skill guarantees:

- **Fresh Start**: Result directory cleared on each `--discover` run
- **Idempotent**: Same inputs always produce same output
- **Sequential**: Steps execute in fixed order
- **Predictable**: No exploratory or adaptive behavior
- **Transparent**: Each step's purpose and output clearly defined
