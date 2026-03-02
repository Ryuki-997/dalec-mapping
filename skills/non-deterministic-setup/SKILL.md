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
    Binaries []Binary `yaml:"binaries"` // All binaries
    Targets  []string `yaml:"targets"` // Build targets (e.g. azlinux3/container)
    
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

### Task 0: Build Targets Selection

**Input:** Dockerfile and Makefile content provided in prompt

**Output:** `Targets` (YAML field in output)

#### 0.1 Allowed Targets

All of the following build targets are supported. Select the ones that apply based on the project's build files:

| Target | Description |
| ------ | ----------- |
| `azlinux3/container` | Azure Linux 3 container image (primary Linux target) |
| `azlinux3/rpm` | Azure Linux 3 RPM package |
| `bookworm/deb` | Debian Bookworm deb package |
| `noble/deb` | Ubuntu Noble deb package |
| `jammy/deb` | Ubuntu Jammy deb package |
| `focal/deb` | Ubuntu Focal deb package |
| `bionic/deb` | Ubuntu Bionic deb package |
| `windowscross/container` | Windows cross-compiled container image |

#### 0.2 Selection Rules

1. **Default:** If the Dockerfile/Makefile does not indicate a specific target platform, emit both `azlinux3/container` and `windowscross/container`.
2. **Windows-only:** If the project only builds for Windows (e.g., `GOOS=windows` exclusively, no Linux binary), emit both `azlinux3/container` and `windowscross/container`.
3. **Additional targets:** If the project explicitly needs RPM or deb packaging, include the relevant targets (e.g., `azlinux3/rpm`, `bookworm/deb`, etc.) in addition to the container targets.
4. **When in doubt**, default to `azlinux3/container` and `windowscross/container` only.

#### 0.3 Extraction Checklist

- [ ] Check Makefile for `GOOS` references — does it build for both `linux` and `windows`?
- [ ] Check Dockerfile for platform-specific instructions
- [ ] Only add rpm/deb targets if explicitly indicated in the build files

#### 0.4 Patterns

```yaml
# Default (most Go projects):
targets:
  - "azlinux3/container"
  - "windowscross/container"
```

---

### Task 1: Binary Output Extraction

**Input:** Makefile and Dockerfile content provided in prompt

- Makefile content - Search for build commands  
- Dockerfile content - Search for COPY and ENTRYPOINT instructions

**Output:** `Binaries` (YAML fields in output)

#### 1.1 Core Principle: Deterministic Build Steps

**CRITICAL:** Each binary extraction MUST produce a single, deterministic build step. The Dockerfile and Makefile contain all the information needed — there is no ambiguity.

**Rules:**
1. **Collapse all conditionals into one path.** If the Makefile/Dockerfile has `if/else` branches for different architectures, OS, or configurations, pick the **primary production path** (the one matching the Dockerfile's final COPY/ENTRYPOINT). Do NOT preserve conditionals or emit multiple alternatives.
2. **Resolve platform-specific output paths.** If the output path contains `${OS}`, `${ARCH}`, `${GOOS}`, `${GOARCH}`, `${TARGETARCH}`, or similar platform variables, **remove them** and collapse to a flat path. Dalec handles platform targeting via its target system, not via path conventions.
   - `bin/${OS}_${ARCH}/kubelogin` → `bin/kubelogin`
   - `_output/${ARCH}/blobplugin` → `_output/blobplugin`
   - `bin/${OS}_${ARCH}${GOARM:+v${GOARM}}/binary` → `bin/binary`
3. **Preserve `${VERSION}`, `${COMMIT}`, `${REVISION}` variables.** These are Dalec spec args and must remain as `${VAR}` references.
4. **No double slashes.** After removing platform variables from paths, ensure no `//` remains. `bin//kubelogin` is invalid — it must be `bin/kubelogin`.
5. **The output path in `buildCommand -o <path>` must exactly match `outputPath`.** These two fields describe the same thing — the file path where `go build` writes the binary. They must be identical so the artifact section can find the built file.

#### 1.2 Multi-Binary Makefile: Selecting the Correct Target

**Scenario:** A single Makefile defines many build targets, each producing a different binary (e.g. `azure-cns`, `azure-npm`, `azure-vnet`, `acncli`). The pipeline generates **one spec per image**, so only the target matching the requested image name is relevant.

**How to identify the correct target:**

1. **Match on image name.** The prompt specifies which image is being built (via `SpecImageName`). Find the Makefile target whose output binary name matches that image name.
2. **Match on Dockerfile ENTRYPOINT/COPY.** If the Dockerfile copies a specific binary into the final stage or sets it as the entrypoint, that is the target binary. The Makefile target producing that binary is the correct one.
3. **Ignore all other targets.** Do NOT extract build commands for unrelated binaries. Only extract the one (or few) that directly produce the requested image's artifacts.

**Rules:**
- Extract **only** the binary whose name matches the image being built.
- If the matching target has prerequisites (e.g. `bpf-lib`), note them but do not extract their build commands as separate binaries.
- The `ldflags`, `outputPath`, and `buildCommand` must come from the matched target only — not from a different target in the same Makefile.

**Example: azure-container-networking Makefile (building `azure-cns` image)**

```makefile
# The Makefile has 12+ binary targets:
azure-block-iptables-binary:
	cd $(AZURE_BLOCK_IPTABLES_DIR) && CGO_ENABLED=0 go build -v -o $(AZURE_BLOCK_IPTABLES_BUILD_DIR)/azure-block-iptables$(EXE_EXT) -ldflags "-X main.version=$(AZURE_BLOCK_IPTABLES_VERSION)"

azure-vnet-binary:
	cd $(CNI_NET_DIR) && CGO_ENABLED=0 go build -v -o $(CNI_BUILD_DIR)/azure-vnet$(EXE_EXT) -ldflags "-X main.version=$(CNI_VERSION) $(LD_BUILD_FLAGS)"

azure-cns-binary:                                           # ← This is the correct target for image "azure-cns"
	cd $(CNS_DIR) && CGO_ENABLED=0 go build -v -o $(CNS_BUILD_DIR)/azure-cns$(EXE_EXT) -ldflags "-X main.version=$(CNS_VERSION) -X $(CNS_AI_PATH)=$(CNS_AI_ID) -X $(CNI_AI_PATH)=$(CNI_AI_ID) $(LD_BUILD_FLAGS)"

azure-npm-binary:
	cd $(CNI_TELEMETRY_DIR) && CGO_ENABLED=0 go build -v -o $(NPM_BUILD_DIR)/azure-npm$(EXE_EXT) -ldflags "-X main.version=$(NPM_VERSION) $(LD_BUILD_FLAGS)"

# ... 8+ more targets
```

The image being built is `azure-cns`. The correct extraction:

```yaml
# ✅ CORRECT: Only the azure-cns target is extracted
binaries:
  - name: "azure-cns"
    outputPath: "output/cns/azure-cns"
    buildCommand: "cd ${CNS_DIR} && go build -v -o output/cns/azure-cns -ldflags \"-X main.version=${VERSION} -X ${CNS_AI_PATH}=${CNS_AI_ID} -X ${CNI_AI_PATH}=${CNI_AI_ID} ${LD_BUILD_FLAGS}\" -gcflags=\"-dwarflocationlists=true\""
    ldFlags: "-X main.version=${VERSION} -X ${CNS_AI_PATH}=${CNS_AI_ID} -X ${CNI_AI_PATH}=${CNI_AI_ID} ${LD_BUILD_FLAGS}"
```

```yaml
# ❌ WRONG: Extracting all 12 binaries from the Makefile
binaries:
  - name: "azure-block-iptables"
    ...
  - name: "azure-vnet"
    ...
  - name: "azure-cns"
    ...
  - name: "azure-npm"
    ...
  # ... 8 more unrelated binaries
```

```yaml
# ❌ WRONG: Picking the wrong target (azure-vnet instead of azure-cns)
binaries:
  - name: "azure-vnet"
    outputPath: "output/cni/azure-vnet"
    buildCommand: "cd ${CNI_NET_DIR} && go build ..."
    ldFlags: "-X main.version=${CNI_VERSION} ${LD_BUILD_FLAGS}"
```

#### 1.3 Extraction Checklist

- [ ] Identify which image/binary is being built (from image name or Dockerfile ENTRYPOINT)
- [ ] If the Makefile has multiple binary targets, select **only** the one matching the image name
- [ ] Find the `go build -o <path>` command in the matched target
- [ ] Extract binary name from `-o` flag path (last path segment)
- [ ] If no `-o` flag, infer from `./cmd/<name>` package path
- [ ] **Collapse conditional branches** — pick the single production build path
- [ ] **Remove platform variables** from output paths (`${OS}`, `${ARCH}`, etc.)
- [ ] **Verify no double slashes** in the resulting path
- [ ] Add matched binaries to binaries list:
  - [ ] Primary = matches Dockerfile ENTRYPOINT or image name
- [ ] Store the cleaned, deterministic path for artifact mapping

#### 1.4 Patterns

```makefile
# Pattern A: Direct -o flag with platform vars (COLLAPSE)
go build -o _output/${ARCH}/blobplugin ./pkg/blobplugin
# → binaries[0].name: "blobplugin"
# → binaries[0].outputPath: "_output/blobplugin"
# → binaries[0].buildCommand: "go build -o _output/blobplugin ./pkg/blobplugin"

# Pattern B: Nested conditional output path (COLLAPSE)
go build -o bin/${OS}_${ARCH}${GOARM:+v${GOARM}}/kubelogin -ldflags "-X main.gitTag=${VERSION}"
# → binaries[0].name: "kubelogin"
# → binaries[0].outputPath: "bin/kubelogin"
# → binaries[0].buildCommand: "go build -o bin/kubelogin -ldflags \"-X main.gitTag=${VERSION}\""
# → binaries[0].ldFlags: "-X main.gitTag=${VERSION}"

# Pattern C: Conditional build (COLLAPSE to primary path)
# if [ "$(ARCH)" = "amd64" ]; then
#   go build -o _output/amd64/binary ./cmd/main
# else
#   go build -o _output/arm64/binary ./cmd/main
# fi
# → binaries[0].outputPath: "_output/binary"
# → binaries[0].buildCommand: "go build -o _output/binary ./cmd/main"

# Pattern D: Variable reference (keep non-platform vars)
go build -o $(TEMP_DIR)/pod_nanny main.go
# → binaries[0].name: "pod_nanny"
# → binaries[0].outputPath: "$(TEMP_DIR)/pod_nanny"

# Pattern E: Implicit (no -o flag)
go build ./cmd/myapp
# → binaries[0].name: "myapp" (inferred from package)
# → binaries[0].outputPath: "myapp"
```

#### 1.5 Anti-Patterns (DO NOT produce these)

```yaml
# ❌ WRONG: Platform variables in output path
outputPath: "bin/${OS}_${ARCH}/kubelogin"

# ❌ WRONG: Double slashes from collapsed variables
outputPath: "bin//kubelogin"
buildCommand: "go build -o bin//kubelogin ./cmd/main"

# ❌ WRONG: Conditional preserved in build command  
buildCommand: "if [ \"$ARCH\" = \"amd64\" ]; then go build -o bin/amd64/app; else go build -o bin/arm64/app; fi"

# ❌ WRONG: outputPath doesn't match buildCommand -o path
outputPath: "bin/kubelogin"
buildCommand: "go build -o bin/${OS}_${ARCH}/kubelogin ..."

# ✅ CORRECT: Single deterministic step
outputPath: "bin/kubelogin"
buildCommand: "go build -o bin/kubelogin -ldflags \"-X main.gitTag=${VERSION}\""
ldFlags: "-X main.gitTag=${VERSION}"
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

#### 4.1 Core Principle: Deterministic Output

The build command in Dockerfile and Makefile is **deterministic** — it has a concrete, observable structure. The LLM's job is to extract it faithfully, not interpret or branch.

**Rules:**
1. **Collapse all IF/ELSE/case branches** into the single primary build path. Dalec handles platform targeting; the build step must be one linear command.
2. **Remove environment variable assignments** that Dalec manages: `CGO_ENABLED=`, `GOOS=`, `GOARCH=`, `GOARM=`, `OS=`, `ARCH=`. These are set in the Dalec spec's `build.env` section.
3. **Remove platform variables from output paths** (`${OS}`, `${ARCH}`, `${TARGETARCH}`, etc.) and collapse any resulting double slashes.
4. **The `-o <path>` in the build command MUST match the `outputPath` field exactly.** Both describe where the binary is written — they must be identical.
5. **Preserve `${VERSION}`, `${COMMIT}`, `${REVISION}`** — these are Dalec spec args.
6. **Replace version-like Makefile variables** (`$(TAG)`, `$(VERSION)`, `$(IMAGE_VERSION)`) with `${VERSION}` in ldflags.
7. **Replace git commit variables** (`$(GIT_COMMIT)`, `$(COMMIT)`) with `${COMMIT}` in ldflags.

#### 4.2 Extraction Checklist

- [ ] Find primary build target:
  1. `.PHONY: container` or `container:` target
  2. `.PHONY: build` or `build:` target
  3. `.PHONY: all` or `all:` target
  4. Direct `go build` command
- [ ] Extract `go build` command from target
- [ ] **Collapse all conditional branches** to a single command
- [ ] Remove Docker wrappers:
  - `docker run golang:... /bin/bash -c "..."`
  - `docker run --rm -v ... go build`
- [ ] Remove env var assignments: `CGO_ENABLED=... GOOS=... GOARCH=...`
- [ ] **Remove platform variables from -o path** and collapse `//` → `/`
- [ ] Parse ldflags:
  - [ ] Extract `-ldflags "..."` content
  - [ ] Replace `$(TAG)`, `$(VERSION)`, `$(IMAGE_VERSION)` with `${VERSION}`
  - [ ] Replace `$(GIT_COMMIT)` with `${COMMIT}`
  - [ ] Preserve `-s -w` strip flags
- [ ] Convert `$(VAR)` to `${VAR}` syntax
- [ ] **Verify `-o <path>` matches `outputPath`**

#### 4.3 Patterns

```makefile
# Makefile input (with conditionals and platform vars):
CGO_ENABLED=${CGO_ENABLED} GOOS=linux GOARCH=$(ARCH) go build -a \
    -ldflags "-X ${PKG}/pkg/version.Ver=$(TAG) -s -w" \
    -o _output/${ARCH}/binary ./cmd/main

# Extracted (deterministic, collapsed):
# BuildCommand: "go build -a -ldflags \"-X ${PKG}/pkg/version.Ver=${VERSION} -s -w\" -o _output/binary ./cmd/main"
# LdFlags: "-X ${PKG}/pkg/version.Ver=${VERSION} -s -w"
# OutputPath: "_output/binary"
```

```makefile
# Makefile input (with IF/ELSE for arch):
# if [ "$(ARCH)" = "amd64" ]; then
#   go build -o bin/amd64/myapp -ldflags "-X main.version=$(VERSION)" ./cmd/myapp
# else
#   go build -o bin/arm64/myapp -ldflags "-X main.version=$(VERSION)" ./cmd/myapp
# fi

# Extracted (collapsed to single step):
# BuildCommand: "go build -o bin/myapp -ldflags \"-X main.version=${VERSION}\" ./cmd/myapp"
# LdFlags: "-X main.version=${VERSION}"
# OutputPath: "bin/myapp"
```

#### 4.4 Dalec Translation

```yaml
build:
  env:
    CGO_ENABLED: "1"
    GOEXPERIMENT: systemcrypto
    GOPROXY: direct
    VERSION: ${VERSION}
  steps:
    - command: |
        cd ${SOURCE_DIR}
        go build -a -ldflags "-X ${PKG}/pkg/version.Ver=${VERSION} -s -w" -o _output/binary ./cmd/main
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
│  - artifacts.binaries (from Binaries list)                       │
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
| V1 | `binaries[0].name` matches Makefile `-o` output (not repo name) | [ ] |
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

binaries:
  - name: "blobplugin"
    outputPath: "_output/blobplugin"
    buildCommand: "go build -a -ldflags \"${LDFLAGS}\" -mod vendor -o _output/blobplugin ./pkg/blobplugin"
    ldFlags: "-X sigs.k8s.io/blob-csi-driver/pkg/blob.driverVersion=${VERSION} -s -w"
  - name: "blobfuse-proxy"
    outputPath: "_output/blobfuse-proxy"
    buildCommand: "go build -o _output/blobfuse-proxy ./pkg/blobfuse-proxy"
    ldFlags: ""
targets:
  - "azlinux3/container"
  - "windowscross/container"

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
    Binaries: []{{Name: "blobplugin", OutputPath: "_output/blobplugin", BuildCommand: "go build -a ...", LdFlags: "..."}, {Name: "blobfuse-proxy", OutputPath: "_output/blobfuse-proxy", ...}},
    Targets:  []string{"azlinux3/container", "windowscross/container"},
    
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
