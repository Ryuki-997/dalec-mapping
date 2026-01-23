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
│  Step 0: Check Repository for Build Files                         │
│  ──────────────────────────────────────────────────────────────── │
│  Action: List remote directory contents to locate files           │
│  Output: examples/{repo}/{subdir}/Dockerfile (if exists)          │
│          examples/{repo}/{subdir}/Makefile (if exists)            │
│  Note: Directory is {repo}/{subdir} if subdir exists, else {repo} │
└───────────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌─────────────────────────────────────────────────────────────────┐
│  Step 1: Extract Non-Deterministic Values                       │
│  ─────────────────────────────────────────────────────────────  │
│  Trigger: Dockerfile AND/OR Makefile downloaded in Step 0       │
│  Action: Search through downloaded files for build values       │
│  Skill: .github/skills/non-deterministic-setup/SKILL.md         │
│  Output: examples/{repo}/{subdir}/NonDeterministicValues.yml    │
└─────────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌─────────────────────────────────────────────────────────────────┐
│  Step 2: Run CLI Tool to Generate Dalec Spec                    │
│  ─────────────────────────────────────────────────────────────  │
│  Command: go run main.go -repo <repo> \                         │
│           -dockerfile examples/{repo}/{subdir}/Dockerfile \     │
│           -makefile examples/{repo}/{subdir}/Makefile \         │
│           -output examples/{repo}/{subdir}/{name}.yml           │
│  Output: examples/{repo}/{subdir}/{name}.yml                    │
└─────────────────────────────────────────────────────────────────┘
```

### Skip Condition for Step 1

If repo source does not contain either Dockerfile or Makefile:

- Skip Step 1 (non-deterministic extraction)
- Proceed directly to Step 2 with empty `NonDeterministicValues`
- CLI will use repository name as binary name (fallback behavior)

---

## Directory Naming Convention

The examples directory structure follows this naming pattern:

| Input Format | Directory Created |
| ------------ | ----------------- |
| `owner/repo` | `examples/{repo}/` |
| `owner/repo/tree/branch` | `examples/{repo}/` |
| `owner/repo/tree/branch/subdir` | `examples/{repo}/{subdir}/` |
| `owner/repo/tree/branch/path/to/subdir` | `examples/{repo}/{subdir}/` (last path component) |

**Examples:**

- `kubernetes/autoscaler` → `examples/autoscaler/`
- `kubernetes/autoscaler/tree/master/addon-resizer` → `examples/autoscaler/addon-resizer/`
- `Azure/azure-cni/tree/main/npm` → `examples/azure-cni/npm/`

---

## Detailed Workflow Steps

### Step 0: Check Repository for Build Files

**Action:** List remote directory contents to precisely locate Dockerfile and Makefile, then download if present

- **Input:** Repository URL or `owner/repo` format (supports `owner/repo/tree/branch/subdir` for monorepos)
- **Operations:**
  1. Parse repository URL to extract: owner, repo, branch, subdirectory (if any)
  2. Determine target directory: `examples/{repo}/{subdir}/` if subdir exists, else `examples/{repo}/`
  3. **List remote directory contents** at the target path (subdir or root) to check for files
  4. Search the directory listing for `Dockerfile` and `Makefile` entries
  5. If Dockerfile exists in listing → Download raw content to local examples directory
  6. If Makefile exists in listing → Download raw content to local examples directory
- **Output:** Downloaded files in `examples/{repo}/{subdir}/` directory
- **Files Downloaded:**
  - `examples/{repo}/{subdir}/Dockerfile` (if exists in remote directory)
  - `examples/{repo}/{subdir}/Makefile` (if exists in remote directory)
- **Validation:**
  - Log file discovery status from directory listing (found/not found)
  - Confirm downloaded files are readable
- **File Location Strategy:**
  - Use raw GitHub URL: `https://raw.githubusercontent.com/{owner}/{repo}/{branch}/{subdir}/Dockerfile`
  - Do NOT make API requests to check existence; use directory listing instead

### Step 1: Extract Non-Deterministic Values (Conditional)

**Trigger:** At least one of Dockerfile or Makefile was downloaded in Step 0

**Action:** Run [non-deterministic-setup](./../non-deterministic-setup/SKILL.md) skill to extract build values

- **Input:** Downloaded files from Step 0
  - `examples/{repo}/{subdir}/Dockerfile`
  - `examples/{repo}/{subdir}/Makefile`
- **Operations:**
  1. Read downloaded Dockerfile (if exists)
  2. Read downloaded Makefile (if exists)
  3. Search for binary name, output path, entrypoint in these files
  4. Extract ldflags, build commands, dependencies
  5. Populate `NonDeterministicValues` struct
  6. Write to `examples/{repo}/{subdir}/NonDeterministicValues.yml`
- **Output:** `examples/{repo}/{subdir}/NonDeterministicValues.yml`
- **Skip If:** Neither Dockerfile nor Makefile was found in Step 0

### Step 2: Run CLI Tool to Generate Dalec Spec

**CRITICAL:** This step is the single source of truth for spec file generation. The agent MUST use the CLI tool.

**Action:** Execute `go run main.go` with the downloaded files to generate the Dalec spec

- **Input:**
  - Repository path
  - Downloaded Dockerfile path
  - Downloaded Makefile path
  - Output path for generated spec
- **Command:**

  ```bash
  go run main.go \
    -repo {owner}/{repo}/tree/{branch}/{subdir} \
    -dockerfile examples/{repo}/{subdir}/Dockerfile \
    -makefile examples/{repo}/{subdir}/Makefile \
    -output examples/{repo}/{subdir}/{name}.yml
  ```
  
- **Operations:**
  - CLI parses Dockerfile and Makefile
  - CLI fetches GitHub metadata
  - CLI reads NonDeterministicValues.yml from same directory as Dockerfile
  - CLI generates complete Dalec spec
  - CLI writes output YAML file
- **Output:** `examples/{repo}/{subdir}/{name}.yml`
- **Validation:**
  - Command exits with status 0
  - Output YAML file exists and is valid YAML
- **Note:** Do NOT manually create the spec file - always use the CLI tool

### Step 3: Fetch GitHub Metadata (CLI Internal)

**Note:** This step is performed internally by the CLI tool (`main.go`). The agent does not perform this step directly.

**Action:** Query GitHub API for repository information

- **Input:** Repository URL or `owner/repo` format (supports `owner/repo/tree/branch/subdir` for monorepos)
- **Operations:**
  - Parse repository path and extract owner/repo/branch/subdirectory
  - Fetch repository metadata via GitHub API
  - Retrieve latest release information
  - Extract commit SHA from release tag
  - Detect source generator (gomod, cargohome, pip, godep)
- **Output:** `RepoInfo` struct with metadata
- **Validation:**
  - Verify all required fields populated (Owner, Repo, GitURL, LatestCommit, Version)

### Step 4: Parse Dockerfile (CLI Internal)

**Note:** This step is performed internally by the CLI tool. The agent does not perform this step directly.

**Action:** Extract build configuration from Dockerfile

- **Input:** Downloaded Dockerfile from Step 0 OR manual path via `-dockerfile` flag
- **Operations:**
  - Parse Dockerfile stages and instructions
  - Extract build arguments (ARG directives)
  - Identify dependencies from base images
  - Extract environment variables
  - **Resolve base image placeholders** (e.g., `FROM BASEIMAGE` → lookup in Makefile)
  - **Extract COPY/ADD instructions** to identify binary artifacts being copied
  - **Parse CMD/ENTRYPOINT** to identify the primary executable
- **Output:** `DockerfileInfo` struct with:
  - `BaseImage`: Resolved base image name
  - `BuildArgs`: Map of ARG values
  - `CopiedArtifacts`: List of files copied into the image
  - `Entrypoint`: Primary executable command
- **Validation:** Check parsing completed without errors; skip if no Dockerfile found

### Step 5: Parse Makefile & Extract Build Artifacts (CLI Internal)

**Note:** This step is performed internally by the CLI tool. The agent does not perform this step directly.

**Action:** Extract build commands, arguments, and artifact output paths from Makefile

- **Input:** Downloaded Makefile from Step 0 OR manual path via `-makefile` flag
- **Operations:**
  - Parse all variables into a map with full resolution (handle nested `$(VAR)` references)
  - **Identify .PHONY targets** and their associated build commands
  - **Search common build targets** in priority order:
    1. `.PHONY: container` / `container:` target
    2. `.PHONY: build` / `build:` target  
    3. `.PHONY: all` / `all:` target
    4. `binary:` or `binaries:` targets
    5. Direct `go build`, `cargo build`, `make` commands
  - **Extract binary output path** from build commands:
    - Go: `-o <path>` flag in `go build` command
    - Rust: Parse `target/release/<binary>` or `--out-dir` flag
    - Generic: Look for `-o`, `--output`, `OUTPUT=` patterns
  - **Resolve placeholder variables** (e.g., `BASEIMAGE`, `TAG`, `VERSION`)
  - Extract ldflags for version injection patterns
- **Output:** `MakefileInfo` struct with:
  - `Variables`: Fully resolved variable map
  - `BuildTargets`: List of identified build targets
  - `BuildCommands`: Actual shell commands for each target
  - `BinaryOutputPath`: Detected artifact output location
  - `LdFlags`: Version/metadata injection flags
- **Validation:**:
  - Verify at least one build target identified
  - Confirm binary output path is determinable
  - Log warning if build target detection is ambiguous

### Step 6: Initialize Default Spec (CLI Internal)

**Note:** This step is performed internally by the CLI tool. The agent does not perform this step directly.

**Action:** Combine GitHub metadata, Dockerfile info, and Makefile info

- **Input:** RepoInfo + DockerfileInfo + MakefileInfo
- **Operations:**
  - Set default revision to 1
  - Configure default build targets (azlinux3/container, azlinux3/rpm, noble/deb)
  - Initialize DefaultSpec structure
  - **Cross-reference artifact paths** between Makefile and Dockerfile
- **Output:** `DefaultSpec` struct
- **Validation:** Ensure all required fields present

### Step 7: Transform to Dalec Spec with Artifact Consistency (CLI Internal)

**Note:** This step is performed internally by the CLI tool. The agent does not perform this step directly.

**Action:** Generate complete Dalec specification with validated artifact paths

- **Input:** DefaultSpec + MakefileInfo.BinaryOutputPath + DockerfileInfo.Entrypoint
- **Operations:**
  - Populate args Common args: (Revision, Version, Commit, TARGETARCH, TARGETOS) and Env Args from makefile if necessary
  - Extract sources with Git URL and generator
  - Add build extensions and targets
  - Setup build steps from dockerfile and makefile if necessary
  - Configure dependencies (build and runtime) by looking into dockerfile and makefile
  - **Define artifacts with consistency validation:**
    - Binary path from Makefile `-o` output OR Dockerfile COPY source
    - Ensure artifact path matches between build output and COPY instruction
  - **Set up image configuration with artifact alignment:**
    - Entrypoint: Must reference a defined artifact binary
    - Symlinks: Auto-generate `/usr/bin/{binary}` → artifact path
    - Validate entrypoint binary exists in artifacts section
  - Generate test specifications
- **Output:** Complete `DalecSpec` map with consistent artifact references
- **Validation:**:
  - Verify artifact binary path matches build output
  - Confirm entrypoint references valid artifact
  - Validate symlink targets exist

### Step 8: Write YAML Output (CLI Internal)

**Note:** This step is performed internally by the CLI tool. The agent does not perform this step directly.

**Action:** Serialize spec to YAML file

- **Input:** DalecSpec
- **Operations:**
  - Add Dalec syntax header
  - Encode to YAML with proper formatting
  - Apply Dalec-specific formatting rules
  - Write to output file (default: `examples/{repo}/{subdir}/{name}.yml`)
- **Output:** YAML file
- **Validation:** Confirm file written successfully

---

## Agent Execution Summary

The agent performs only **3 steps**:

1. **Step 0:** List remote directory, locate files, download Dockerfile/Makefile
2. **Step 1:** Extract non-deterministic values and write NonDeterministicValues.yml
3. **Step 2:** Run `go run main.go` CLI command to generate the Dalec spec

Steps 3-8 are handled internally by the CLI tool and should NOT be performed manually by the agent.

---

## Artifact Discovery & Consistency

### Build Artifact Detection Strategy

The tool uses a multi-source approach to identify the correct binary output path:

#### Priority Order for Binary Path Detection

1. **Makefile `-o` flag** (highest confidence)

   ```makefile
   go build -o $(TEMP_DIR)/pod_nanny main.go
   # Extracts: pod_nanny
   ```

2. **Makefile OUTPUT/BINARY variable**

   ```makefile
   BINARY = myapp
   OUTPUT_DIR = /go/bin
   # Extracts: /go/bin/myapp
   ```

3. **Dockerfile COPY instruction** (for pre-built binaries)

   ```dockerfile
   COPY pod_nanny /
   # Extracts: pod_nanny → /pod_nanny
   ```

4. **Dockerfile ENTRYPOINT/CMD** (fallback)

   ```dockerfile
   CMD /pod_nanny
   # Infers: /pod_nanny is the binary
   ```

#### .PHONY Target Parsing

For Makefiles with .PHONY declarations, extract build commands from these targets:

```makefile
.PHONY: container build all

container: .container-$(ARCH)
.container-$(ARCH): buildx-setup
    # Build command here - EXTRACT THIS
    go build -o $(OUTPUT)/binary main.go
```

**Extraction Rules:**

- Follow target dependencies recursively
- Look for actual shell commands (lines starting with tab)
- Parse `go build`, `cargo build`, `make`, `gcc`, etc.
- Extract `-o` output path from build commands

### Artifact Consistency Matrix

The tool validates that artifacts are consistent across all references:

| Source | Field | Must Match |
| ------ | ----- | ---------- |
| Makefile | `-o <path>` output | → `artifacts.binaries` key |
| Dockerfile | `COPY <src> <dest>` | → `artifacts.binaries` value |
| Dockerfile | `ENTRYPOINT ["/bin"]` | → `image.entrypoint` |
| Dalec Spec | `image.entrypoint` | → Must exist in `artifacts.binaries` |
| Dalec Spec | `image.post.symlinks` | → Target must match artifact path |

#### Example Consistency Flow

```bash
Makefile:     go build -o /go/bin/azure-cns ...
                           ↓
Dockerfile:   COPY /go/bin/azure-cns /
                           ↓
Dalec Spec:   
  artifacts:
    binaries:
      /go/bin/azure-cns: {}    ← Must match Makefile output
  image:
    entrypoint: /azure-cns      ← Must reference artifact
    post:
      symlinks:
        /usr/bin/azure-cns:
          path: /azure-cns      ← Must match entrypoint
```

### Validation Errors

The tool reports errors when consistency is broken:

- ❌ `ARTIFACT_MISMATCH`: Makefile output path doesn't match Dockerfile COPY
- ❌ `ENTRYPOINT_NOT_FOUND`: Entrypoint binary not in artifacts
- ❌ `SYMLINK_TARGET_MISSING`: Symlink points to non-existent path
- ⚠️ `AMBIGUOUS_OUTPUT`: Multiple possible binary outputs detected

## Execution

### Command Syntax

```bash
go run main.go -repo <repository> -dockerfile <path> -makefile <path> -output <file> [-v]
```

### Required Parameters

- `-repo`: GitHub repository (formats: `owner/repo`, `https://github.com/owner/repo`, `owner/repo/tree/branch`, `owner/repo/tree/branch/subdir`)
- `-dockerfile`: Path to downloaded Dockerfile
- `-makefile`: Path to downloaded Makefile
- `-output`: Output YAML file path

### Optional Parameters

- `-v`: Verbose output

### Example Usage

```bash
# Monorepo subdirectory (e.g., kubernetes/autoscaler addon-resizer)
# After downloading files to examples/autoscaler/addon-resizer/
go run main.go \
  -repo kubernetes/autoscaler/tree/master/addon-resizer \
  -dockerfile examples/autoscaler/addon-resizer/Dockerfile \
  -makefile examples/autoscaler/addon-resizer/Makefile \
  -output examples/autoscaler/addon-resizer/addon-resizer.yml

# Simple repo without subdirectory
# After downloading files to examples/myrepo/
go run main.go \
  -repo microsoft/myrepo \
  -dockerfile examples/myrepo/Dockerfile \
  -makefile examples/myrepo/Makefile \
  -output examples/myrepo/myrepo.yml
```

## Validation Checklist

After generation, verify the spec file:

### 1. Required Fields Present

- [ ] `name`: Repository name (lowercase)
- [ ] `version`: `${VERSION}` variable reference
- [ ] `revision`: `${REVISION}` variable reference
- [ ] `sources`: Git URL and commit SHA
- [ ] `artifacts`: Binary and license paths
- [ ] `dependencies`: Build and runtime dependencies
- [ ] `targets`: At least one build target defined

### 2. Args Section

- [ ] `Commit`: Valid SHA hash
- [ ] `Revision`: Integer value
- [ ] `Version`: Unquoted version
- [ ] `TARGETARCH`: Empty or valid architecture
- [ ] `TARGETOS`: Empty or valid OS

### 3. Sources Section

- [ ] Git URL is accessible
- [ ] Commit SHA exists in repository
- [ ] Generator matches project type (gomod/cargohome/pip)

### 4. Artifacts Section

- [ ] Binary path derived from Makefile `-o` output or Dockerfile COPY
- [ ] Binary path format consistent: e.g., `/go/bin/{binary}` or `/{binary}`
- [ ] License path format: `{repo}/LICENSE`
- [ ] Artifact path matches build command output

### 5. Image Configuration (for container targets)

- [ ] Entrypoint defined and references a valid artifact binary
- [ ] Symlinks created for binaries in `/usr/bin/`
- [ ] Symlink target path matches artifact binary location
- [ ] Entrypoint command is executable (not a directory)

### 6. Artifact Consistency Validation

- [ ] Makefile output path == Artifacts binaries key
- [ ] Dockerfile COPY destination == Image entrypoint path
- [ ] Symlink target == Entrypoint path
- [ ] No orphaned artifacts (all artifacts referenced somewhere)

### 6. Tests Section

- [ ] File permission checks present
- [ ] Permission values unquoted octal (e.g., `0755`)

## Build and Test

### 1. Validate Generated Spec

```bash
# Check YAML syntax
cat examples/{repo-name}/{repo-name}.yml

# Verify all placeholders resolved
grep -E '\$\{[A-Z_]+\}' examples/{repo-name}/{repo-name}.yml
```

### 2. Build with Dalec

Requires `azcu` binary (Azure Container Upstream wrapper):

```bash
# Build azcu tool
make build

# Process spec and build artifacts
./bin/azcu-darwin-arm64 process-spec examples/{repo-name}/{repo-name}.yml \
  --actions build \
  --output-base-dir /tmp/azcu \
  --targets azlinux3/container \
  --debug \
  --trace
```

### 3. Alternative: Docker Buildx

```bash
# Build directly with Docker Buildx
docker buildx build -f examples/{repo-name}/{repo-name}.yml .
```

## Error Handling

The tool exits with non-zero status codes on failure:

- **Missing required flag**: `-repo` flag not provided
- **GitHub API errors**: Repository not found, rate limiting, network issues
- **File download errors**: Dockerfile/Makefile not found in repo (warning only, continues)
- **Dockerfile parse errors**: Invalid Dockerfile syntax
- **Makefile parse errors**: Unable to resolve variables or find build targets
- **Artifact consistency errors**: Mismatch between build output and image configuration
- **YAML write errors**: Permission denied, disk full

### Warning Conditions (Non-Fatal)

- ⚠️ No Dockerfile found in repository
- ⚠️ No Makefile found in repository
- ⚠️ Multiple .PHONY targets detected, using first match
- ⚠️ Binary output path inferred (not explicitly detected)
- ⚠️ Entrypoint not found, using default `/{repo-name}`

## Success Criteria

A successful run produces:

1. ✅ No error messages during execution
2. ✅ Output YAML file created at specified location
3. ✅ Downloaded files saved to `examples/{repo-name}/` (if auto-download enabled)
4. ✅ All validation checks pass
5. ✅ Artifact consistency validated (binary → entrypoint → symlink)
6. ✅ Spec builds successfully with azcu/docker buildx

## Directory Structure After Run

```bash
dalec-mapping/
├── examples/
│   └── {repo}/
│       └── {subdir}/              # Only if subdirectory specified
│           ├── Dockerfile         # Downloaded from GitHub
│           ├── Makefile           # Downloaded from GitHub
│           ├── NonDeterministicValues.yml # Agent-extracted values
│           └── {name}.yml         # Generated Dalec spec (via CLI)
└── ...
```

**Example for `kubernetes/autoscaler/tree/master/addon-resizer`:**

```bash
dalec-mapping/
├── examples/
│   └── autoscaler/
│       └── addon-resizer/
│           ├── Dockerfile
│           ├── Makefile
│           ├── NonDeterministicValues.yml
│           └── addon-resizer.yml
└── ...
```

**Example for `microsoft/myrepo` (no subdirectory):**

```bash
dalec-mapping/
├── examples/
│   └── myrepo/
│       ├── Dockerfile
│       ├── Makefile
│       ├── NonDeterministicValues.yml
│       └── myrepo.yml
└── ...
```

## Deterministic Behavior

This agent skill guarantees:

- **Idempotent**: Same inputs always produce same output
- **Sequential**: Steps execute in fixed order
- **Predictable**: No exploratory or adaptive behavior
- **Transparent**: Each step's purpose and output clearly defined
- **Validatable**: Each step has verification criteria
