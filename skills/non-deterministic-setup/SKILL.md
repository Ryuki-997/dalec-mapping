---
name: non-deterministic-setup
description: LLM skill for parsing Dockerfile and Makefile into NonDeterministicValues YAML format
---

# Non-Deterministic Setup - LLM Skill

This skill enables LLMs to systematically parse Dockerfile and Makefile content into a structured `NonDeterministicValues` format. Variable values that cannot be determined by fixed rules are extracted and formatted as YAML.

**Input:** Dockerfile and Makefile content provided directly via LLM prompts

**Output:** NonDeterministicValues YAML structure written to `result/{repo-name}/NonDeterministicValues.yml`

These extracted values populate the `NonDeterministicValues` struct in `transformer/agent.go` and are later used by `main.go` to fill the Dalec spec.

---

## Data Structures

### NonDeterministicValues Struct

The Go struct uses yaml tags that **require camelCase keys** in the YAML file:

```go
// parser/agentValues.go
type NonDeterministicValues struct {
    // Build Artifacts
    BinaryName        string            `yaml:"binaryName"`        // Primary binary name
    BinaryOutputPath  string            `yaml:"binaryOutputPath"`  // Full output path
    Binaries []Binary `yaml:"binaries"` // All binaries
    
    // Image Configuration  
    Entrypoint        string            `yaml:"entrypoint"`        // Container entrypoint
    Symlink           string            `yaml:"symlink"`           // Symlink path
    
    // Dependencies
    BuildDeps         []string          `yaml:"buildDeps"`         // Build-time dependencies
    RuntimeDeps       []string          `yaml:"runtimeDeps"`       // Runtime dependencies
    ExternalTools     []ExternalTool    `yaml:"externalTools"`     // curl/wget downloaded tools
    
    // Validation
    Warnings          []string          `yaml:"warnings"`          // Agent review warnings
    Confidence        float64           `yaml:"confidence"`        // Extraction confidence (0.0-1.0)
}

type ExternalTool struct {
    Name              string `yaml:"name"`              // Tool name (e.g., "azcopy")
    DownloadURL       string `yaml:"downloadURL"`       // Source URL
    NeedsSeparateSpec bool   `yaml:"needsSeparateSpec"` // Requires separate Dalec spec
}

type Binary struct {
    Name         string `yaml:"name"`         // Binary name
    OutputPath   string `yaml:"outputPath"`   // Output path
    BuildCommand string `yaml:"buildCommand"` // Build command
    LdFlags      string `yaml:"ldFlags"`      // LdFlags for this binary
}
```

**CRITICAL:** When writing `NonDeterministicValues.yml`, use the yaml tag names (camelCase), NOT the Go field names (PascalCase).

### Output Location

The LLM must generate and return the extracted values as YAML in the following format:

```bash
result/{repo-name}/NonDeterministicValues.yml
```

Where `{repo-name}` is the repository name (e.g., `kubelogin`, `blob-csi-driver`). The YAML output is then persisted to this location in the result directory.

---

## Agent Extraction Tasks

### Task 1: Binary Output Extraction

**Input:** Makefile and Dockerfile content provided in prompt

- Makefile content - Search for build commands  
- Dockerfile content - Search for COPY and ENTRYPOINT instructions

**Output:** `BinaryName`, `BinaryOutputPath`, `Binaries` (YAML fields in output)

#### 1.1 Extraction Checklist

- [ ] Find all `go build -o <path>` commands in Makefile
- [ ] Extract binary name from `-o` flag path (last path segment)
- [ ] If no `-o` flag, infer from `./cmd/<name>` package path
- [ ] Add all binaries to binary list:
  - [ ] Primary = matches Dockerfile ENTRYPOINT or repo name
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

**Input:** Dockerfile content provided in prompt  
**Output:** `Entrypoint`, `Symlink` (YAML fields in output)

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

**Input:** Dockerfile and Makefile content provided in prompt

- Dockerfile content - Search for apt/yum install commands  
- Makefile content - Search for image references

**Output:** `BuildDeps`, `RuntimeDeps`, `ExternalTools` (YAML fields in output)

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

**Input:** Makefile content provided in prompt  
**Output:** `BuildCommand`, `LdFlags` (YAML fields in output)

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

## LLM Tool Interface

This skill defines the systematic parsing functions that LLMs implement:

```go
// transformer/agent.go - LLM-based extraction functions

// ExtractBinaryOutput parses go build commands from Makefile
func ExtractBinaryOutput(makefileContent string) (primary string, path string, auxiliaries []string, err error)

// ExtractEntrypoint parses ENTRYPOINT/CMD from Dockerfile
func ExtractEntrypoint(dockerfileContent string) (entrypoint string, symlink string, err error)

// ExtractDependencies identifies build vs runtime dependencies from Dockerfile and Makefile
func ExtractDependencies(dockerfileContent, makefileContent string) (build []string, runtime []string, external []ExternalTool, err error)

// ExtractBuildCommand translates Makefile build target to Dalec format
func ExtractBuildCommand(makefileContent string) (command string, ldflags string, env map[string]string, err error)

// ValidateExtraction checks consistency and assigns confidence score
func ValidateExtraction(values *NonDeterministicValues) (warnings []string, confidence float64)

// FillNonDeterministicValues orchestrates all LLM extractions
func FillNonDeterministicValues(dockerfileContent, makefileContent string) (*NonDeterministicValues, error)
```

**Note:** LLMs implement these functions by parsing the provided Dockerfile and Makefile content (via prompt) and returning structured YAML output.

---

## Workflow Integration

```bash
┌─────────────────────────────────────────────────────────────────┐
│                   LLM Processing Pipeline                       │
└─────────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌─────────────────────────────────────────────────────────────────┐
│  Step 1: LLM-based Non-Deterministic Setup                      │
│  ─────────────────────────────────────────────────────────────  │
│  Input: Dockerfile and Makefile content via prompt              │
│  Process:                                                       │
│     ├── ExtractBinaryOutput()                                   │
│     ├── ExtractEntrypoint()                                     │
│     ├── ExtractDependencies()                                   │
│     ├── ExtractBuildCommand()                                   │
│     └── ValidateExtraction()                                    │
│  Output: NonDeterministicValues YAML structure                  │
│  Location: result/{repo-name}/NonDeterministicValues.yml        │
└─────────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌─────────────────────────────────────────────────────────────────┐
│  Step 2: Deterministic Workflow (dalec-spec-generator)          │
│  ─────────────────────────────────────────────────────────────  │
│  Input: NonDeterministicValues.yml from result/                 │
│  Uses values to populate:                                       │
│  - artifacts.binaries (from BinaryName, BinaryOutputPath)       │
│  - image.entrypoint (from Entrypoint)                           │
│  - image.post.symlinks (from Symlink)                           │
│  - dependencies.build (from BuildDeps)                          │
│  - dependencies.runtime (from RuntimeDeps)                      │
│  - build.steps (from binary BuildCommand, LdFlags)              │
│  CLI fills remaining deterministic fields (license, version)    │
│  Output: Final Dalec YAML spec (result/output.yml)              │
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
| V8 | All `Binaries` have corresponding build commands | [ ] |
| V9 | `ExternalTools` documented with TODO comments | [ ] |
| V10 | `Confidence` >= 0.8 or manual review completed | [ ] |

---

## Example Extraction

### Input Files (Provided via LLM Prompt)

**Makefile content:**

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

**Dockerfile content:**

```dockerfile
FROM golang:1.21 AS builder
RUN apt install -y curl

FROM debian:bookworm
RUN apt install -y ca-certificates fuse
COPY _output/${ARCH}/blobplugin /blobplugin
ENTRYPOINT ["/blobplugin"]
```

### Extracted Values (YAML Output)

**IMPORTANT:** YAML keys must be **camelCase** to match Go struct tags.

```yaml
# Output: result/{repo-name}/NonDeterministicValues.yml
# YAML keys are camelCase (not PascalCase)

binaryName: "blobplugin"
binaryOutputPath: "_output/${ARCH}/blobplugin"
binaries:
  - name: "blobplugin"
    outputPath: "blobplugin"
    buildCommand: "go build -a -ldflags \"${LDFLAGS}\" -mod vendor -o blobplugin ./pkg/blobplugin"
    ldFlags: "-X sigs.k8s.io/blob-csi-driver/pkg/blob.driverVersion=${VERSION} -s -w"
  - name: "blobfuse-proxy"
    outputPath: "_output/${ARCH}/blobfuse-proxy"
    buildCommand: "go build -o _output/${ARCH}/blobfuse-proxy ./pkg/blobfuse-proxy"
    ldFlags: ""

entrypoint: "/blobplugin"
symlink: "/usr/bin/blobplugin"

buildDeps:
  - "msft-golang"
  - "curl"
runtimeDeps:
  - "ca-certificates"
  - "fuse"
externalTools: []

warnings:
  - "WARN_MULTI_BINARY: blobfuse-proxy detected"
confidence: 0.85
```

Equivalent Go struct (for reference):

```go
NonDeterministicValues{
    BinaryName:        "blobplugin",
    BinaryOutputPath:  "_output/${ARCH}/blobplugin", 
    Binaries: []{{Name: "blobfuse-proxy", ...}},
    
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
