---
name: Non-Deterministic Setup
description: Agent skill for extracting variable values from Dockerfile and Makefile before Dalec spec generation
---

# Non-Deterministic Setup - Agent Skill

Before any parsing or algorithm runs, the agent must extract variable values that cannot be determined by fixed rules. These values populate the `NonDeterministicValues` struct in `transformer/agent.go` and are later used by `main.go` to fill the Dalec spec.

**Prerequisite:** The dalec-spec-generator Step 0 must have completed, downloading available files to:
- `examples/{repo-name}/Dockerfile` (if exists in repo)
- `examples/{repo-name}/Makefile` (if exists in repo)

This skill reads from these downloaded files to extract non-deterministic values.

---

## Data Structures

### NonDeterministicValues Struct

```go
// transformer/agent.go
type NonDeterministicValues struct {
    // Build Artifacts
    BinaryName        string            // Primary binary name (e.g., "blobplugin")
    BinaryOutputPath  string            // Full output path (e.g., "_output/${ARCH}/blobplugin")
    AuxiliaryBinaries []string          // Additional binaries produced
    
    // Image Configuration  
    Entrypoint        string            // Container entrypoint (e.g., "/blobplugin")
    Symlink           string            // Symlink path (e.g., "/usr/bin/blobplugin")
    
    // Dependencies
    BuildDeps         []string          // Build-time dependencies
    RuntimeDeps       []string          // Runtime dependencies (filtered)
    ExternalTools     []ExternalTool    // curl/wget downloaded tools
    
    // Build Configuration
    BuildCommand      string            // Primary go build command
    LdFlags           string            // Translated ldflags with ${VERSION}
    
    // Validation
    Warnings          []string          // Agent review warnings
    Confidence        float64           // Extraction confidence score (0.0 - 1.0)
}

type ExternalTool struct {
    Name              string  // Tool name (e.g., "azcopy")
    DownloadURL       string  // Source URL
    NeedsSeparateSpec bool    // Requires separate Dalec spec
}
```

### Output Location

The agent must write the extracted values to:

```
examples/{repo-name}/NonDeterministicValues.yml
```

Where `{repo-name}` is the repository name (e.g., `blob-csi-driver`). This ensures the file is co-located with the downloaded Dockerfile and Makefile.

---

## Agent Extraction Tasks

### Task 1: Binary Output Extraction

**Input:** Downloaded files from `examples/{repo-name}/` directory:
- `examples/{repo-name}/Makefile` - Search for build commands  
- `examples/{repo-name}/Dockerfile` - Search for COPY and ENTRYPOINT instructions

**Output:** `BinaryName`, `BinaryOutputPath`, `AuxiliaryBinaries`

#### 1.1 Extraction Checklist

- [ ] Find all `go build -o <path>` commands in Makefile
- [ ] Extract binary name from `-o` flag path (last path segment)
- [ ] If no `-o` flag, infer from `./cmd/<name>` package path
- [ ] If multiple binaries found:
  - [ ] Primary = matches Dockerfile ENTRYPOINT or repo name
  - [ ] Others = add to auxiliary binaries list
- [ ] Store full path for artifact mapping

#### 1.2 Patterns

```makefile
# Pattern A: Direct -o flag
go build -o _output/${ARCH}/blobplugin ./pkg/blobplugin
# → BinaryName: "blobplugin"
# → BinaryOutputPath: "_output/${ARCH}/blobplugin"

# Pattern B: Variable reference
go build -o $(TEMP_DIR)/pod_nanny main.go
# → BinaryName: "pod_nanny"
# → BinaryOutputPath: "$(TEMP_DIR)/pod_nanny"

# Pattern C: Implicit (no -o flag)
go build ./cmd/myapp
# → BinaryName: "myapp" (inferred from package)
# → BinaryOutputPath: "myapp"
```

---

### Task 2: Entrypoint & Symlink

**Input:** `examples/{repo-name}/Dockerfile` (downloaded file)  
**Output:** `Entrypoint`, `Symlink`

#### 2.1 Extraction Checklist

- [ ] Find last `ENTRYPOINT` or `CMD` instruction in final Dockerfile stage
- [ ] Parse command: array form `["/bin"]` or shell form `/bin`
- [ ] Extract executable path (first element if array)
- [ ] Derive symlink: `/usr/bin/<binary-name>` → `<entrypoint>`
- [ ] Verify entrypoint matches extracted binary name

#### 2.2 Patterns

```dockerfile
# Pattern A: ENTRYPOINT array form
ENTRYPOINT ["/blobplugin"]
# → Entrypoint: "/blobplugin"
# → Symlink: "/usr/bin/blobplugin"

# Pattern B: CMD shell form
CMD /pod_nanny
# → Entrypoint: "/pod_nanny"
# → Symlink: "/usr/bin/pod_nanny"

# Pattern C: ENTRYPOINT with arguments
ENTRYPOINT ["/app", "--config", "/etc/app.conf"]
# → Entrypoint: "/app"
# → Symlink: "/usr/bin/app"
```

---

### Task 3: Dependencies Extraction

**Input:** Downloaded files from `examples/{repo-name}/` directory:  
- `examples/{repo-name}/Dockerfile` - Search for apt/yum install commands  
- `examples/{repo-name}/Makefile` - Search for image references

**Output:** `BuildDeps`, `RuntimeDeps`, `ExternalTools`

#### 3.1 Build Dependencies Checklist

- [ ] Check Dockerfile builder stage for `apt install` packages
- [ ] Check Makefile for `golang:` image → add `msft-golang`
- [ ] Check for `rust:` image → add `rust`
- [ ] Extract C library deps: `libssl-dev`, `pkg-config`, etc.
- [ ] Add `curl` if used in builder stage

#### 3.2 Runtime Dependencies Checklist

- [ ] Analyze **final Dockerfile stage only** (not builder stages)
- [ ] Find `apt install`, `yum install`, `apk add` commands
- [ ] Apply exclusion filter:

| Exclude | Reason |
| ------- | ------ |
| `fi`, `then`, `else`, `do`, `done`, `;;` | Shell syntax |
| `&&`, `\|\|`, `;` | Operators |
| `install`, `dpkg`, `apt`, `apt-get` | Commands |
| `/path/to/file` | File paths |
| `$VAR`, `${VAR}` | Variables |

- [ ] Handle conditionals: extract all branches, add arch comments

#### 3.3 External Tools Checklist

- [ ] Find `curl -L`, `curl -Ls`, `wget` commands downloading binaries
- [ ] Extract tool name from URL path
- [ ] Mark as `NeedsSeparateSpec: true`
- [ ] Add TODO comment in generated spec

#### 3.4 Patterns

```dockerfile
# Build deps (builder stage)
FROM golang:1.21 AS builder
RUN apt install -y curl libssl-dev
# → BuildDeps: ["msft-golang", "curl", "libssl-dev"]

# Runtime deps (final stage)
RUN apt install -y ca-certificates curl
# → RuntimeDeps: ["ca-certificates", "curl"]

# Conditional deps
RUN if [ "$ARCH" = "amd64" ]; then apt install -y blobfuse; fi
# → RuntimeDeps: ["blobfuse"] with comment "# amd64 only"

# External tools
RUN curl -Ls https://github.com/Azure/azcopy/releases/.../azcopy.tar.gz | tar xz
# → ExternalTools: [{Name: "azcopy", NeedsSeparateSpec: true}]
```

---

### Task 4: Build Command Translation

**Input:** `examples/{repo-name}/Makefile` (downloaded file)  
**Output:** `BuildCommand`, `LdFlags`

#### 4.1 Extraction Checklist

- [ ] Find primary build target:
  1. `.PHONY: container` or `container:` target
  2. `.PHONY: build` or `build:` target
  3. `.PHONY: all` or `all:` target
  4. Direct `go build` command
- [ ] Extract `go build` command from target
- [ ] Remove Docker wrappers:
  - `docker run golang:... /bin/bash -c "..."`
  - `docker run --rm -v ... go build`
- [ ] Parse ldflags:
  - [ ] Extract `-ldflags "..."` content
  - [ ] Replace `$(TAG)`, `$(VERSION)`, `$(IMAGE_VERSION)` with `${VERSION}`
  - [ ] Replace `$(GIT_COMMIT)` with `${COMMIT}`
  - [ ] Preserve `-s -w` strip flags
- [ ] Convert `$(VAR)` to `${VAR}` syntax

#### 4.2 Patterns

```makefile
# Makefile input:
CGO_ENABLED=1 GOOS=linux GOARCH=$(ARCH) go build -a \
    -ldflags "-X ${PKG}/pkg/version.Ver=$(TAG) -s -w" \
    -o _output/binary ./cmd/main

# Extracted:
# BuildCommand: "go build -a -ldflags \"...\" -o _output/binary ./cmd/main"
# LdFlags: "-X ${PKG}/pkg/version.Ver=${VERSION} -s -w"
```

#### 4.3 Dalec Translation

```yaml
build:
  env:
    CGO_ENABLED: "1"
    GOOS: linux
  steps:
    - command: |
        cd ${SOURCE_DIR}
        go build -a -ldflags "-X ${PKG}/pkg/version.Ver=${VERSION} -s -w" -o binary ./cmd/main
```

---

### Task 5: Validation & Confidence Scoring

**Input:** All extracted values  
**Output:** `Warnings`, `Confidence`

#### Confidence Scoring

| Condition | Score |
| --------- | ----- |
| Binary name matches entrypoint binary | +0.25 |
| Symlink target matches entrypoint | +0.15 |
| Runtime deps filtered correctly (no shell syntax) | +0.20 |
| Build command has no `docker run` | +0.20 |
| LdFlags translated to `${VERSION}` | +0.10 |
| No external tools requiring separate specs | +0.10 |

**Minimum confidence for auto-approval:** 0.8

#### Warning Codes

| Code | Condition | Action Required |
| ---- | --------- | --------------- |
| `WARN_MULTI_BINARY` | Multiple binaries detected | Verify primary binary |
| `WARN_CONDITIONAL_DEPS` | Architecture-specific deps | Review for target arch |
| `WARN_EXTERNAL_TOOLS` | External tools found | Create separate Dalec specs |
| `WARN_NO_LDFLAGS` | No version injection found | May need manual ldflags |
| `WARN_DOCKER_WRAPPER` | Build uses docker run | Needs manual translation |
| `WARN_ENTRYPOINT_MISMATCH` | Entrypoint != binary name | Verify correct binary |
| `WARN_LOW_CONFIDENCE` | Confidence < 0.8 | Manual review required |

#### Agent Tool

```go
func ValidateExtraction(values *NonDeterministicValues) (warnings []string, confidence float64)
```

---

## Agent Tool Interface

```go
// transformer/agent.go

// ExtractBinaryOutput finds -o flag in go build commands
func ExtractBinaryOutput(makefileContent string) (primary string, path string, auxiliaries []string, err error)

// ExtractEntrypoint parses ENTRYPOINT/CMD from Dockerfile
func ExtractEntrypoint(dockerfileContent string) (entrypoint string, symlink string, err error)

// ExtractDependencies separates build vs runtime deps
func ExtractDependencies(dockerfileContent, makefileContent string) (build []string, runtime []string, external []ExternalTool, err error)

// ExtractBuildCommand translates Makefile target to Dalec build steps
func ExtractBuildCommand(makefileContent string) (command string, ldflags string, env map[string]string, err error)

// ValidateExtraction checks consistency and assigns confidence
func ValidateExtraction(values *NonDeterministicValues) (warnings []string, confidence float64)

// FillNonDeterministicValues orchestrates all extractions
func FillNonDeterministicValues(dockerfileContent, makefileContent string) (*NonDeterministicValues, error)
```

---

## Workflow Integration

```bash
┌─────────────────────────────────────────────────────────────────┐
│                     main.go Entry Point                         │
└─────────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌─────────────────────────────────────────────────────────────────┐
│  Step 0: Non-Deterministic Setup (Agent Pre-Processing)         │
│  ─────────────────────────────────────────────────────────────  │
│  1. Read raw Dockerfile and Makefile content                    │
│  2. Call FillNonDeterministicValues()                           │
│     ├── ExtractBinaryOutput()                                   │
│     ├── ExtractEntrypoint()                                     │
│     ├── ExtractDependencies()                                   │
│     ├── ExtractBuildCommand()                                   │
│     └── ValidateExtraction()                                    │
│  3. Log warnings for human review                               │
│  4. If confidence < 0.8, prompt for manual verification         │
│  Output: NonDeterministicValues struct                          │
└─────────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌─────────────────────────────────────────────────────────────────┐
│  Steps 1-6: Deterministic Workflow (dalec-spec-generator)       │
│  ─────────────────────────────────────────────────────────────  │
│  Uses NonDeterministicValues to populate:                       │
│  - artifacts.binaries (from BinaryName, BinaryOutputPath)       │
│  - image.entrypoint (from Entrypoint)                           │
│  - image.post.symlinks (from Symlink)                           │
│  - dependencies.build (from BuildDeps)                          │
│  - dependencies.runtime (from RuntimeDeps)                      │
│  - build.steps (from BuildCommand, LdFlags)                     │
│  CLI fills remaining deterministic fields (license, version)    │
│  Generates final Dalec YAML spec                                │
└─────────────────────────────────────────────────────────────────┘
```

---

## Post-Extraction Validation Checklist

After agent extraction, verify:

| # | Check | Status |
| --- | ------- | -------- |
| V1 | `BinaryName` matches Makefile `-o` output (not repo name) | [ ] |
| V2 | `Entrypoint` matches Dockerfile `ENTRYPOINT`/`CMD` | [ ] |
| V3 | `Symlink` target matches `Entrypoint` path | [ ] |
| V4 | `RuntimeDeps` excludes: `fi`, `then`, `;`, `install`, `dpkg` | [ ] |
| V5 | `BuildDeps` includes compiler (`msft-golang` / `rust`) | [ ] |
| V6 | `LdFlags` uses `${VERSION}` not hardcoded values | [ ] |
| V7 | `BuildCommand` has no `docker run` commands | [ ] |
| V8 | All `AuxiliaryBinaries` have corresponding build commands | [ ] |
| V9 | `ExternalTools` documented with TODO comments | [ ] |
| V10 | `Confidence` >= 0.8 or manual review completed | [ ] |

---

## Example Extraction

### Input Files

**Makefile:**

```makefile
PKG = sigs.k8s.io/blob-csi-driver
IMAGE_VERSION ?= v1.28.0
LDFLAGS ?= "-X ${PKG}/pkg/blob.driverVersion=${IMAGE_VERSION} -s -w"

.PHONY: blob
blob:
    CGO_ENABLED=1 GOOS=linux GOARCH=$(ARCH) go build -a -ldflags ${LDFLAGS} -mod vendor -o _output/${ARCH}/blobplugin ./pkg/blobplugin

.PHONY: blobfuse-proxy
blobfuse-proxy:
    CGO_ENABLED=1 go build -o _output/${ARCH}/blobfuse-proxy ./pkg/blobfuse-proxy
```

**Dockerfile:**

```dockerfile
FROM golang:1.21 AS builder
RUN apt install -y curl

FROM debian:bookworm
RUN apt install -y ca-certificates fuse
COPY _output/${ARCH}/blobplugin /blobplugin
ENTRYPOINT ["/blobplugin"]
```

### Extracted Values

```go
NonDeterministicValues{
    BinaryName:        "blobplugin",
    BinaryOutputPath:  "_output/${ARCH}/blobplugin",
    AuxiliaryBinaries: []string{"blobfuse-proxy"},
    
    Entrypoint:        "/blobplugin",
    Symlink:           "/usr/bin/blobplugin",
    
    BuildDeps:         []string{"msft-golang", "curl"},
    RuntimeDeps:       []string{"ca-certificates", "fuse"},
    ExternalTools:     []ExternalTool{},
    
    BuildCommand:      "go build -a -ldflags \"${LDFLAGS}\" -mod vendor -o blobplugin ./pkg/blobplugin",
    LdFlags:           "-X sigs.k8s.io/blob-csi-driver/pkg/blob.driverVersion=${VERSION} -s -w",
    
    Warnings:          []string{"WARN_MULTI_BINARY: blobfuse-proxy detected"},
    Confidence:        0.85,
}
```
