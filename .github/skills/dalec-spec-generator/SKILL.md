---
name: Dalec Spec Generator
description: Agent skill for generating Dalec specification files from GitHub repositories with deterministic workflow
---

# Dalec Spec Generator - Agent Skill

This tool implements a deterministic agent skill for converting GitHub repositories into Dalec specification files for container and package builds.

## Deterministic Workflow

The tool follows a fixed, predictable sequence of operations:

### Step 1: Fetch GitHub Metadata

**Action:** Query GitHub API for repository information

- **Input:** Repository URL or `owner/repo` format
- **Operations:**
  - Parse repository path and extract owner/repo/branch
  - Fetch repository metadata via GitHub API
  - Retrieve latest release information
  - Extract commit SHA from release tag
  - Detect source generator (gomod, cargohome, pip)
- **Output:** `RepoInfo` struct with metadata
- **Validation:** Verify all required fields populated (Owner, Repo, GitURL, LatestCommit, Version)

### Step 2: Parse Dockerfile (Optional)

**Action:** Extract build configuration from Dockerfile if provided

- **Input:** Path to Dockerfile (optional via `-dockerfile` flag)
- **Operations:**
  - Parse Dockerfile stages and instructions
  - Extract build arguments (ARG directives)
  - Identify dependencies from base images
  - Extract environment variables
- **Output:** `DockerfileInfo` struct
- **Validation:** Check parsing completed without errors; skip if no Dockerfile provided

### Step 3: Parse Makefile (Optional)

**Action:** Extract arguments used by Dockerfile as well as potential output path

- **Input:** Path to Makefile (optional via `-makefile` flag)
- **Operations:**
  - Parse all values into a map
  - Get the true (non-nested) value of the argument
  - Set build steps underneath the build section through targets like `container`
  - Acquire potential binary output path
- **Output** `MakefileInfo` struct, output path string
- **Validation** Verify build steps can be completed and provide valid binary output; skip if no Makefile provided

### Step 4: Initialize Default Spec

**Action:** Combine GitHub metadata and Dockerfile info

- **Input:** RepoInfo + DockerfileInfo
- **Operations:**
  - Set default revision to 1
  - Configure default build targets (azlinux3/container, azlinux3/rpm, noble/deb)
  - Initialize DefaultSpec structure
- **Output:** `DefaultSpec` struct
- **Validation:** Ensure all required fields present

### Step 4: Transform to Dalec Spec

**Action:** Generate complete Dalec specification

- **Input:** DefaultSpec
- **Operations:**
  - Populate args Common args: (Revision, Version, Commit, TARGETARCH, TARGETOS) and Env Args from makefile if necessary
  - Extract sources with Git URL and generator
  - Add build extensions and targets
  - Setup build steps from dockerfile and makefile if necessary
  - Configure dependencies (build and runtime) by looking into dockerfile and makefile
  - Define artifacts (binaries, licenses)
  - Set up image configuration (entrypoint, symlinks)
  - Generate test specifications
- **Output:** Complete `DalecSpec` map
- **Validation:** Verify all sections populated correctly

### Step 5: Write YAML Output

**Action:** Serialize spec to YAML file

- **Input:** DalecSpec
- **Operations:**
  - Add Dalec syntax header
  - Encode to YAML with proper formatting
  - Apply Dalec-specific formatting rules
  - Write to output file (default: `output.yml`)
- **Output:** YAML file
- **Validation:** Confirm file written successfully

## Execution

### Command Syntax

```bash
go run main.go -repo <repository> [-dockerfile <path>] [-output <file>] [-v]
```

### Required Parameters

- `-repo`: GitHub repository (formats: `owner/repo`, `https://github.com/owner/repo`, `owner/repo/tree/branch`)

### Optional Parameters

- `-dockerfile`: Path to Dockerfile (default: none)
- `-output`: Output YAML file path (default: `output.yml`)
- `-v`: Verbose output

### Example Usage

```bash
# Basic: Generate spec from GitHub repo only
go run main.go -repo microsoft/azurelinuxagent

# With Dockerfile
go run main.go -repo owner/repo -dockerfile ./Dockerfile -output spec.yml

# With branch
go run main.go -repo owner/repo/tree/develop
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

- [ ] Binary path format: `{repo}/bin/{repo}`
- [ ] License path format: `{repo}/LICENSE`

### 5. Image Configuration (for container targets)

- [ ] Entrypoint defined
- [ ] Symlinks created for binaries in `/usr/bin/`

### 6. Tests Section

- [ ] File permission checks present
- [ ] Permission values unquoted octal (e.g., `0755`)

## Build and Test

### 1. Validate Generated Spec

```bash
# Check YAML syntax
cat output.yml

# Verify all placeholders resolved
grep -E '\$\{[A-Z_]+\}' output.yml
```

### 2. Build with Dalec

Requires `azcu` binary (Azure Container Upstream wrapper):

```bash
# Build azcu tool
make build

# Process spec and build artifacts
./bin/azcu-darwin-arm64 process-spec output.yml \
  --actions build \
  --output-base-dir /tmp/azcu \
  --targets azlinux3/container \
  --debug \
  --trace
```

### 3. Alternative: Docker Buildx

```bash
# Build directly with Docker Buildx
docker buildx build -f output.yml .
```

## Error Handling

The tool exits with non-zero status codes on failure:

- **Missing required flag**: `-repo` flag not provided
- **GitHub API errors**: Repository not found, rate limiting, network issues
- **Dockerfile parse errors**: Invalid Dockerfile syntax
- **YAML write errors**: Permission denied, disk full

## Success Criteria

A successful run produces:

1. ✅ No error messages during execution
2. ✅ Output YAML file created at specified location
3. ✅ All validation checks pass
4. ✅ Spec builds successfully with azcu/docker buildx

## Deterministic Behavior

This agent skill guarantees:

- **Idempotent**: Same inputs always produce same output
- **Sequential**: Steps execute in fixed order
- **Predictable**: No exploratory or adaptive behavior
- **Transparent**: Each step's purpose and output clearly defined
- **Validatable**: Each step has verification criteria
