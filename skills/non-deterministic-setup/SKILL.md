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

    // Targets is the ordered list of build targets.
    // Each entry is a TargetSpec containing the OS, entrypoint, symlink, and deps.
    Targets  []TargetSpec `yaml:"targets"`

    ExternalTools []ExternalTool `yaml:"externalTools"`

    // Validation
    Warnings   []string `yaml:"warnings"`
    Confidence float64  `yaml:"confidence"`
}

// TargetSpec holds ALL per-target configuration in one struct.
type TargetSpec struct {
    TargetOS   string   `yaml:"targetOS"`   // e.g. "azlinux3/container"
    Entrypoint string   `yaml:"entrypoint"` // absolute path inside the image
    Symlink    string   `yaml:"symlink"`    // secondary path → Entrypoint (Linux only)
    Build      []string `yaml:"build"`      // app-specific compile-time packages
    Runtime    []string `yaml:"runtime"`    // app-specific runtime packages (empty for windowscross)
}

type ExternalTool struct {
    Name              string `yaml:"name"`
    DownloadURL       string `yaml:"downloadURL"`
    NeedsSeparateSpec bool   `yaml:"needsSeparateSpec"`
}

type Binary struct {
    Name         string `yaml:"name"`
    OutputPath   string `yaml:"outputPath"`
    BuildCommand string `yaml:"buildCommand"`
    LdFlags      string `yaml:"ldFlags"`
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

### Task 0: Build Targets Configuration

**Input:** Dockerfile and Makefile content provided in prompt

**Output:** `Targets` — a list of `TargetSpec` objects (YAML field in output)

Each build target is represented as a self-contained `TargetSpec` that groups together:
- `targetOS` — the Dalec target string
- `entrypoint` — the binary path inside the container image (target-specific)
- `symlink` — a secondary path pointing to the entrypoint (Linux only)
- `build` — application-specific compile-time packages
- `runtime` — application-specific runtime packages (always empty for `windowscross`)

The transformer automatically adds toolchain and crypto packages on top of what the LLM emits — do NOT include them:

| Auto-added package | Target | Reason |
| ------------------ | ------ | ------ |
| `msft-golang` | all targets (build) | Go toolchain |
| `SymCrypt` | azlinux3 (build + runtime) | FIPS crypto lib |
| `SymCrypt-OpenSSL` | azlinux3 (build + runtime) | systemcrypto provider |
| `openssl-libs` | azlinux3 (build + runtime) | CGO link |

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

1. **Default (cross-platform):** If the Dockerfile/Makefile builds for both Linux and Windows (or does not indicate a specific target platform), emit both `azlinux3/container` and `windowscross/container`.
2. **Windows-only:** If the project only builds for Windows (e.g., `GOOS=windows` exclusively, Dockerfile final stage is Windows-based, no Linux binary produced), emit **only** `windowscross/container`. Do NOT add `azlinux3/container`.
3. **Linux-only:** If the project only builds for Linux (no `GOOS=windows`, no Windows Dockerfile stage, no windows cross-compile target in Makefile), emit **only** `azlinux3/container`. Do NOT add `windowscross/container`.
4. **Additional targets:** If the project explicitly needs RPM or deb packaging, include the relevant targets (e.g., `azlinux3/rpm`, `bookworm/deb`, etc.) in addition to the container targets.
5. **When in doubt**, default to `azlinux3/container` and `windowscross/container` only.

#### 0.3 Extraction Checklist

- [ ] Check Makefile for `GOOS` references — does it build for both `linux` and `windows`?
- [ ] Check Dockerfile for platform-specific instructions and ENTRYPOINT/CMD per stage
- [ ] For each target: determine entrypoint path, symlink, and any extra deps
- [ ] Only add rpm/deb targets if explicitly indicated in the build files
- [ ] `windowscross.runtime` must always be empty
- [ ] Do NOT emit: `msft-golang`, `SymCrypt`, `SymCrypt-OpenSSL`, `openssl-libs`

#### 0.4 Patterns

```yaml
# Cross-platform (default — most Go projects that run on both Linux and Windows):
targets:
  - targetOS: "azlinux3/container"
    entrypoint: "/usr/local/bin/myapp"   # from Dockerfile ENTRYPOINT of Linux final stage
    symlink: "/usr/bin/myapp"            # typically /usr/bin/<binary-name>
    build: []                            # app-specific build packages only
    runtime:                             # app-specific runtime packages only
      - "ca-certificates"
  - targetOS: "windowscross/container"
    entrypoint: "myapp"                  # just the binary name, no path prefix, NO .exe suffix — transformer adds it
    symlink: ""                          # no symlinks on Windows
    build: []
    runtime: []                          # always empty — Dalec rejects runtime deps on Windows

# Windows-only (e.g. kubectl credential plugin):
targets:
  - targetOS: "windowscross/container"
    entrypoint: "myapp"
    symlink: ""
    build: []
    runtime: []

# Linux-only (e.g. Linux daemon/agent with no Windows target):
targets:
  - targetOS: "azlinux3/container"
    entrypoint: "/usr/local/bin/myapp"
    symlink: "/usr/bin/myapp"
    build: []
    runtime:
      - "iptables"
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
2. **Resolve platform-specific output paths.** If the output path contains `${OS}`, `${ARCH}`, `${GOOS}`, `${GOARCH}`, `${TARGETARCH}`, `$(GOOS)`, `$(GOARCH)`, `$(ARCH)`, `$(OS)`, or any variable that expands to a platform string (e.g. `linux`, `windows`, `amd64`, `arm64`), **strip the entire platform segment from the path** and collapse the result. Dalec handles platform targeting via its target system — the `outputPath` must be a single platform-neutral path.
   **CRITICAL — Platform variables MUST NOT appear in `outputPath` or `buildCommand -o <path>` in any form:**
   - `bin/${GOOS}_${GOARCH}/kubelogin` → `bin/kubelogin`
   - `_output/${ARCH}/blobplugin` → `_output/blobplugin`
   - `bin/${OS}_${ARCH}${GOARM:+v${GOARM}}/binary` → `bin/binary`
   - `out/$(GOOS)/$(GOARCH)/myapp` → `out/myapp`
   - After removal, collapse any `//` or trailing `/` that results.
   **CRITICAL — No `.exe` in `outputPath` or `name`:** Never add `.exe` to a binary's `outputPath` or `name` field, even when the Makefile uses `$(EXE_EXT)` or `${EXE_EXT}`. The transformer deterministically appends `.exe` to every `windowscross/container` artifact path and entrypoint — the LLM-emitted values must always use the bare binary name.
3. **Preserve `${VERSION}`, `${COMMIT}`, `${REVISION}` variables.** These are Dalec spec args and must remain as `${VAR}` references.
4. **No double slashes.** After removing platform variables from paths, ensure no `//` remains. `bin//kubelogin` is invalid — it must be `bin/kubelogin`.
5. **`outputPath` is always `/go/bin/<binary-name>` and the `-o` flag in `buildCommand` must match it exactly.** The `buildCommand` writes to `/go/bin/<binary-name>` and `outputPath` records the same. See Rule 7.
6. **Resolve Makefile directory variables to their actual defined value.** When the `-o` flag uses a Makefile variable for the output directory (e.g. `$(CNS_BUILD_DIR)`, `$(CNI_BUILD_DIR)`), you MUST look up that variable's definition in the Makefile and substitute the literal string. Do NOT invent a path by using the binary name as the directory name — that is always wrong.
   - `CNS_BUILD_DIR := output/cns` → `-o output/cns/azure-cns` ✅
   - Guessing `output/azure-cns/azure-cns` because the binary is named `azure-cns` ❌
7. **Always set `outputPath` to `/go/bin/<binary-name>` and use the same path in `buildCommand -o`.** This path always exists in every Go builder image (it is `$GOPATH/bin`), requires no `mkdir`, and works correctly regardless of which subdir the build `cd`s into. Never use a relative path like `output/cns/azure-cns` — the parent directory may not exist in the build sandbox.
8. **Single `command:` block.** The entire build for a binary must be one string: `cd <subdir> && go build -o /go/bin/<name> <flags> <pkg>`. Do NOT split into multiple lines that become separate `command:` entries — each `command:` runs in its own shell.

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
# ✅ CORRECT: Only the azure-cns target is extracted, *_VERSION normalized to ${VERSION}
binaries:
  - name: "azure-cns"
    outputPath: "/go/bin/azure-cns"
    buildCommand: "cd ${CNS_DIR} && go build -v -o /go/bin/azure-cns -ldflags \"-X main.version=${VERSION} -X ${CNS_AI_PATH}=${CNS_AI_ID} -X ${CNI_AI_PATH}=${CNI_AI_ID} ${LD_BUILD_FLAGS}\" -gcflags=\"-dwarflocationlists=true\""
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
# ❌ WRONG: Picking the wrong target (azure-vnet instead of azure-cni)
binaries:
  - name: "azure-vnet"
    outputPath: "output/cni/azure-vnet"
    buildCommand: "cd ${CNI_NET_DIR} && go build ..."
    ldFlags: "-X main.version=${VERSION} ${LD_BUILD_FLAGS}"
```

#### 1.3 Extraction Checklist

- [ ] Identify which image/binary is being built (from image name or Dockerfile ENTRYPOINT)
- [ ] If the Makefile has multiple binary targets, select **only** the one matching the image name
- [ ] Find the `go build -o <path>` command in the matched target
- [ ] Extract binary name from `-o` flag path (last path segment)
- [ ] If no `-o` flag, infer from `./cmd/<name>` package path
- [ ] **Collapse conditional branches** — pick the single production build path
- [ ] **Resolve all Makefile directory variables** used in the `-o` flag — look up each variable's definition (e.g. `CNS_BUILD_DIR := output/cns`) and substitute the literal value; never guess a directory name from the binary name
- [ ] **Remove platform variables** from output paths (`${OS}`, `${ARCH}`, etc.)
- [ ] **Verify no double slashes** in the resulting path
- [ ] Add matched binaries to binaries list:
  - [ ] Primary = matches Dockerfile ENTRYPOINT or image name
- [ ] **Do NOT add `.exe`** to `name` or `outputPath` — the transformer appends `.exe` to all `windowscross/container` artifacts automatically
- [ ] **Set `outputPath` to `/go/bin/<binary-name>`** — always use this absolute path
- [ ] `buildCommand -o` path matches `outputPath` exactly: `/go/bin/<binary-name>`
- [ ] Store the cleaned, deterministic path for artifact mapping

#### 1.4 Patterns

```makefile
# Pattern A: Direct -o flag with platform vars (COLLAPSE → /go/bin/)
go build -o _output/${ARCH}/blobplugin ./pkg/blobplugin
# → binaries[0].name: "blobplugin"
# → binaries[0].outputPath: "/go/bin/blobplugin"
# → binaries[0].buildCommand: "go build -o /go/bin/blobplugin ./pkg/blobplugin"

# Pattern B: Nested conditional output path (COLLAPSE → /go/bin/)
go build -o bin/${OS}_${ARCH}${GOARM:+v${GOARM}}/kubelogin -ldflags "-X main.gitTag=${VERSION}"
# → binaries[0].name: "kubelogin"
# → binaries[0].outputPath: "/go/bin/kubelogin"
# → binaries[0].buildCommand: "go build -o /go/bin/kubelogin -ldflags \"-X main.gitTag=${VERSION}\""
# → binaries[0].ldFlags: "-X main.gitTag=${VERSION}"

# Pattern C: Conditional build (COLLAPSE to primary path)
# if [ "$(ARCH)" = "amd64" ]; then
#   go build -o _output/amd64/binary ./cmd/main
# else
#   go build -o _output/arm64/binary ./cmd/main
# fi
# → binaries[0].outputPath: "/go/bin/binary"
# → binaries[0].buildCommand: "go build -o /go/bin/binary ./cmd/main"

# Pattern D: Makefile directory variable — MUST resolve to actual definition, then rewrite to /go/bin/
# CNS_BUILD_DIR := output/cns
# azure-cns-binary:
#   cd $(CNS_DIR) && go build -o $(CNS_BUILD_DIR)/azure-cns$(EXE_EXT) ...
#
# The resolved native path would be output/cns/azure-cns, but that directory may not exist.
# Always rewrite outputPath to /go/bin/<binaryname>:
# → binaries[0].name: "azure-cns"
# → binaries[0].outputPath: "/go/bin/azure-cns"
# → binaries[0].buildCommand: "cd ${CNS_DIR} && go build -v -o /go/bin/azure-cns -ldflags \"...\" ..."
#
# Artifact key (transformer): /go/bin/azure-cns   (Linux)
#                              /go/bin/azure-cns.exe  (windowscross)
#
# ❌ WRONG: keeping the original relative path — output/cns/ may not exist in build sandbox
# → binaries[0].outputPath: "output/cns/azure-cns"
# → binaries[0].buildCommand: "cd ${CNS_DIR} && go build -o output/cns/azure-cns ..."


go build -o $(TEMP_DIR)/pod_nanny main.go
# → binaries[0].name: "pod_nanny"
# → binaries[0].outputPath: "$(TEMP_DIR)/pod_nanny"

# Pattern E (canonical): Always output to /go/bin/<binary-name>
# Applies to ALL patterns above regardless of what the Makefile specifies.
#
# → binaries[0].outputPath: "/go/bin/azure-cns"
# → binaries[0].buildCommand: "cd ${CNS_DIR} && go build -v -o /go/bin/azure-cns -ldflags \"...\" ..."
#
# /go/bin/ always exists, absolute, works from any cd subdir.
# Artifact keys: /go/bin/azure-cns (global/Linux), /go/bin/azure-cns.exe (windowscross)

# Pattern F: Implicit (no -o flag)
go build ./cmd/myapp
# → binaries[0].name: "myapp" (inferred from package)
# → binaries[0].outputPath: "myapp"
```

#### 1.5 Anti-Patterns (DO NOT produce these)

```yaml
# ❌ WRONG: Any platform variable remaining in outputPath or buildCommand -o path
outputPath: "_output/${ARCH}/blobplugin"
outputPath: "out/$(GOOS)/$(GOARCH)/myapp"
buildCommand: "go build -o bin/${GOOS}_${GOARCH}/kubelogin ..."

# ✅ CORRECT: Platform segments fully stripped
outputPath: "bin/kubelogin"
outputPath: "_output/blobplugin"
outputPath: "out/myapp"
buildCommand: "go build -o bin/kubelogin ..."

# ❌ WRONG: Directory variable guessed from binary name instead of resolved from Makefile definition
# Makefile has: CNS_BUILD_DIR := output/cns
outputPath: "output/azure-cns/azure-cns"   # invented — azure-cns is the binary name, NOT the dir
buildCommand: "cd ${CNS_DIR} && go build -o output/azure-cns/azure-cns ..."

# ✅ CORRECT: CNS_BUILD_DIR looked up in Makefile → literal value is "output/cns"
outputPath: "output/cns/azure-cns"
buildCommand: "cd ${CNS_DIR} && go build -o output/cns/azure-cns ..."

# ❌ WRONG: .exe in outputPath or name — transformer adds it for windowscross automatically
outputPath: "bin/kubelogin.exe"
name: "kubelogin.exe"
buildCommand: "go build -o bin/kubelogin.exe ..."

# ❌ WRONG: Double slashes from collapsed variables
outputPath: "bin//kubelogin"
buildCommand: "go build -o bin//kubelogin ./cmd/main"

# ❌ WRONG: Split build command (BIN_SUFFIX in separate step — each command: runs its own shell)
# buildCommand emitting multiple lines that would become separate command: entries
buildCommand: |-
  BIN_SUFFIX=""
  if [ "${GOOS}" = "windows" ]; then BIN_SUFFIX=".exe"; fi
  cd ${CNS_DIR} && go build -o output/cns/azure-cns${BIN_SUFFIX} ...

# ❌ WRONG: Relative path — output/cns/ may not exist in the build sandbox
outputPath: "output/cns/azure-cns"
buildCommand: "cd ${CNS_DIR} && go build -v -o output/cns/azure-cns -ldflags \"...\" ..."

# ✅ CORRECT: /go/bin/ path — always exists, no mkdir needed, works from any cd subdir
outputPath: "/go/bin/azure-cns"
buildCommand: "cd ${CNS_DIR} && go build -v -o /go/bin/azure-cns -ldflags \"...\" ..."

# ❌ WRONG: Conditional preserved in build command  
buildCommand: "if [ \"$ARCH\" = \"amd64\" ]; then go build -o bin/amd64/app; else go build -o bin/arm64/app; fi"

# ❌ WRONG: outputPath doesn't match buildCommand -o path
outputPath: "bin/kubelogin"
buildCommand: "go build -o bin/${OS}_${ARCH}/kubelogin ..."

# ✅ CORRECT: Single deterministic step using /go/bin/ as canonical output
outputPath: "/go/bin/kubelogin"
buildCommand: "go build -o /go/bin/kubelogin -ldflags \"-X main.gitTag=${VERSION}\""
ldFlags: "-X main.gitTag=${VERSION}"
```

---

### Task 2: Entrypoint & Symlink

**Input:** Dockerfile content provided in prompt  
**Output:** `entrypoint` and `symlink` fields **inside each TargetSpec** (not as top-level fields)

For each build target, determine the correct entrypoint and symlink:
- **Linux targets** (`azlinux3/container`, etc.): entrypoint is the full absolute path (e.g. `/usr/local/bin/azure-cns`); symlink is typically `/usr/bin/<binary-name>`.
- **Windows targets** (`windowscross/container`): entrypoint is just the binary name with no path prefix (e.g. `azure-cns`); symlink should be empty string.

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

**Output:** `build` and `runtime` fields **inside each TargetSpec** (not as a separate `perTargetDeps` map)

For each build target, identify the app-specific packages and place them directly in the target's `build`/`runtime` arrays.

#### 3.0 What the Transformer Already Provides (Do NOT emit these)

The transformer **automatically** adds these to every target — you must NOT include them in `perTargetDeps`:

| Package | Target | Why auto-added |
| ------- | ------ | -------------- |
| `msft-golang` | all targets (build) | Go toolchain, always needed |
| `SymCrypt` | azlinux3 (build + runtime) | FIPS crypto lib, CGO_ENABLED=1 |
| `SymCrypt-OpenSSL` | azlinux3 (build + runtime) | GOEXPERIMENT=systemcrypto provider |
| `openssl-libs` | azlinux3 (build + runtime) | OpenSSL shared libs for CGO link |

Only emit **application-specific** packages that the Dockerfile installs on top of these.

#### 3.1 Structure

`build` and `runtime` are arrays inside each `TargetSpec` in the `targets` list:

```yaml
targets:
  - targetOS: "azlinux3/container"
    entrypoint: "/usr/local/bin/myapp"
    symlink: "/usr/bin/myapp"
    build:           # app-specific compile-time packages
      - "curl"
    runtime:         # app-specific runtime packages
      - "ca-certificates"
      - "iptables"
  - targetOS: "windowscross/container"
    entrypoint: "myapp"
    symlink: ""
    build: []
    runtime: []      # always empty — Dalec rejects runtime deps on Windows
```

**CRITICAL:** `windowscross.runtime` must **always be empty or `[]`**. Dalec rejects runtime deps on Windows output images.

#### 3.2 Per-Target Build Dependencies Checklist

- [ ] Check Dockerfile builder stage for `apt install` / `RUN` install commands
- [ ] Check Makefile for extra build tools (e.g. `curl` in builder, `pkg-config`)
- [ ] Do NOT emit: `msft-golang`, `gcc`, `SymCrypt`, `SymCrypt-OpenSSL`, `openssl-libs` (auto-added)
- [ ] Only emit packages specific to the application's build process

#### 3.3 Per-Target Runtime Dependencies Checklist

- [ ] Analyze **final Dockerfile stage only** (not builder stages)
- [ ] Find `apt install`, `yum install`, `apk add`, `tdnf install` commands
- [ ] Apply exclusion filter:

| Exclude | Reason |
| ------- | ------ |
| `fi`, `then`, `else`, `do`, `done`, `;;` | Shell syntax |
| `&&`, `\|\|`, `;` | Operators |
| `install`, `dpkg`, `apt`, `apt-get`, `tdnf` | Commands |
| `/path/to/file` | File paths |
| `$VAR`, `${VAR}` | Variables |
| `SymCrypt`, `SymCrypt-OpenSSL`, `openssl-libs` | Auto-added by transformer |

- [ ] **Never populate `windowscross.runtime`** — Dalec rejects it
- [ ] Linux-only packages (e.g. `iptables`) go to `azlinux3.runtime` only

#### 3.4 External Tools Checklist

- [ ] Find `curl -L`, `curl -Ls`, `wget` commands downloading binaries
- [ ] Extract tool name from URL path
- [ ] Mark as `NeedsSeparateSpec: true`

#### 3.5 Patterns

```dockerfile
# Builder stage
FROM golang:1.21 AS builder
RUN apt install -y curl

# Final Linux stage
RUN apt install -y ca-certificates iptables
ENTRYPOINT ["/usr/local/bin/myapp"]

# Final Windows stage
COPY myapp.exe /myapp.exe

# → targets:
#   - targetOS: "azlinux3/container"
#     entrypoint: "/usr/local/bin/myapp"
#     symlink: "/usr/bin/myapp"
#     build: ["curl"]           # extra build tool
#     runtime: ["ca-certificates", "iptables"]
#   - targetOS: "windowscross/container"
#     entrypoint: "myapp"   # bare binary name — NO .exe, transformer adds it
#     symlink: ""
#     build: []                 # nothing extra beyond auto-added msft-golang
#     runtime: []               # always empty
```

```dockerfile
# Final stage — no extra build tools, just runtime
RUN apt install -y ca-certificates fuse
ENTRYPOINT ["/usr/local/bin/myapp"]

# → targets:
#   - targetOS: "azlinux3/container"
#     entrypoint: "/usr/local/bin/myapp"
#     symlink: "/usr/bin/myapp"
#     build: []
#     runtime: ["ca-certificates", "fuse"]
#   - targetOS: "windowscross/container"
#     entrypoint: "myapp"
#     symlink: ""
#     build: []
#     runtime: []
```

```yaml
# ❌ WRONG: old perTargetDeps map format (no longer used)
perTargetDeps:
  azlinux3:
    build: []
    runtime:
      - "iptables"
  windowscross:
    build: []

# ✅ CORRECT: per-target fields inside each TargetSpec
targets:
  - targetOS: "azlinux3/container"
    entrypoint: "/usr/local/bin/myapp"
    symlink: "/usr/bin/myapp"
    build: []
    runtime:
      - "iptables"
  - targetOS: "windowscross/container"
    entrypoint: "myapp"
    symlink: ""
    build: []
    runtime: []
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
5. **Convert `$(VAR)` Makefile syntax to `${VAR}` Dalec syntax.** For all variables *except* version variables, keep names exactly as they appear. **Version variables are the exception: any variable whose sole purpose is to carry the image version** (`$(CNS_VERSION)`, `$(CNI_VERSION)`, `$(NPM_VERSION)`, `$(TAG)`, `$(IMAGE_VERSION)`, or any `*_VERSION` / `*_TAG` pattern) **must be replaced with `${VERSION}`**. `${VERSION}` is always available as a top-level Dalec arg — there is no reason to pass through a Makefile-local name.

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
  - [ ] Preserve `-s -w` strip flags and all other flags verbatim
- [ ] Convert `$(VAR)` to `${VAR}` syntax — **replace any `*_VERSION`/`*_TAG` variable with `${VERSION}`; keep all other variable names as-is**
- [ ] **Verify `-o <path>` matches `outputPath`**

#### 4.3 Patterns

```makefile
# Makefile input (with conditionals and platform vars):
CGO_ENABLED=${CGO_ENABLED} GOOS=linux GOARCH=$(ARCH) go build -a \
    -ldflags "-X ${PKG}/pkg/version.Ver=$(TAG) -s -w" \
    -o _output/${ARCH}/binary ./cmd/main

# Extracted (deterministic, collapsed — *_VERSION/TAG normalized to ${VERSION}):
# BuildCommand: "go build -a -ldflags \"-X ${PKG}/pkg/version.Ver=${VERSION} -s -w\" -o _output/binary ./cmd/main"
# LdFlags: "-X ${PKG}/pkg/version.Ver=${VERSION} -s -w"
# OutputPath: "_output/binary"
```

```makefile
# Makefile input (with IF/ELSE for arch):
# if [ "$(ARCH)" = "amd64" ]; then
#   go build -o bin/amd64/myapp -ldflags "-X main.version=$(CNI_VERSION)" ./cmd/myapp
# else
#   go build -o bin/arm64/myapp -ldflags "-X main.version=$(CNI_VERSION)" ./cmd/myapp
# fi

# Extracted (collapsed to single step — *_VERSION normalized to ${VERSION}):
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
| LdFlags uses `${VAR}` syntax (not `$(VAR)`) | +0.10 |
| No external tools requiring separate specs | +0.10 |

**Minimum confidence for auto-approval:** 0.8

#### Warning Codes

| Code | Condition | Action Required |
| ---- | --------- | --------------- |
| `WARN_MULTI_BINARY` | Multiple binaries detected | Verify primary binary |
| `WARN_CONDITIONAL_DEPS` | Architecture-specific deps | Review for target arch |
| `WARN_EXTERNAL_TOOLS` | External tools found | Create separate Dalec specs |
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
│  - targets.<os>.dependencies.build (from PerTargetDeps + auto)  │
│  - targets.<os>.dependencies.runtime (from PerTargetDeps + auto)│
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
| V4 | `targets[azlinux3].runtime` excludes: `fi`, `then`, `;`, `install`, `dpkg`, `SymCrypt`, `openssl-libs` | [ ] |
| V5 | `targets[windowscross].runtime` is empty or `[]` | [ ] |
| V6 | `LdFlags` uses `${VAR}` Dalec syntax (not `$(VAR)` Makefile syntax) | [ ] |
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
    outputPath: "/go/bin/blobplugin"
    buildCommand: "go build -a -ldflags \"${LDFLAGS}\" -mod vendor -o /go/bin/blobplugin ./pkg/blobplugin"
    ldFlags: "-X sigs.k8s.io/blob-csi-driver/pkg/blob.driverVersion=${VERSION} -s -w"
  - name: "blobfuse-proxy"
    outputPath: "/go/bin/blobfuse-proxy"
    buildCommand: "go build -o /go/bin/blobfuse-proxy ./pkg/blobfuse-proxy"
    ldFlags: ""

targets:
  - targetOS: "azlinux3/container"
    entrypoint: "/blobplugin"
    symlink: "/usr/bin/blobplugin"
    build:
      - "curl"              # used in builder stage
    runtime:
      - "ca-certificates"
      - "fuse"
  - targetOS: "windowscross/container"
    entrypoint: "blobplugin"  # bare binary name only — NO .exe, NO path prefix; transformer adds .exe
    symlink: ""               # no symlinks on Windows
    build: []                 # nothing extra beyond auto-added msft-golang
    runtime: []               # always empty — Dalec rejects runtime deps on Windows

externalTools: []

warnings:
  - "WARN_MULTI_BINARY: blobfuse-proxy detected"
confidence: 0.85
```

Equivalent Go struct (for reference):

```go
NonDeterministicValues{
    Binaries: []Binary{
        {Name: "blobplugin", OutputPath: "_output/blobplugin", BuildCommand: "go build -a ...", LdFlags: "..."},
        {Name: "blobfuse-proxy", OutputPath: "_output/blobfuse-proxy", BuildCommand: "go build ..."},
    },
    Targets: []TargetSpec{
        {
            TargetOS:   "azlinux3/container",
            Entrypoint: "/blobplugin",
            Symlink:    "/usr/bin/blobplugin",
            Build:      []string{"curl"},
            Runtime:    []string{"ca-certificates", "fuse"},
        },
        {
            TargetOS:   "windowscross/container",
            Entrypoint: "blobplugin",
            Symlink:    "",
            Build:      []string{},
            Runtime:    []string{},   // intentionally empty
        },
    },
    ExternalTools: []ExternalTool{},
    Warnings:      []string{"WARN_MULTI_BINARY: blobfuse-proxy detected"},
    Confidence:    0.85,
}
```
