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
// llm/llmDTO.go
type NonDeterministicValues struct {
    // Build Artifacts — one entry per go build in the primary build stage.
    Binaries []Binary `yaml:"binaries"`

    // PipelineSteps are ordered shell commands for intermediate and wrapper
    // stages that run AFTER the primary binaries are built.
    // Covers file gathering, compression, embedding, and the wrapper go build.
    // Each entry is one shell command (may be multi-line with \n).
    // Omit when there are no intermediate/wrapper stages.
    PipelineSteps []string `yaml:"pipelineSteps,omitempty"`

    // Targets is the ordered list of build targets.
    // Each entry is a TargetSpec containing the OS, entrypoint, symlink, and deps.
    Targets  []TargetSpec `yaml:"targets"`
}

// TargetSpec holds ALL per-target configuration in one struct.
type TargetSpec struct {
    TargetOS   string   `yaml:"targetOS"`   // e.g. "azlinux3/container"
    Entrypoint string   `yaml:"entrypoint"` // absolute path inside the image
    Symlink    string   `yaml:"symlink"`    // secondary path → Entrypoint (Linux only)
    Build      []string `yaml:"build"`      // app-specific compile-time packages
    Runtime    []string `yaml:"runtime"`    // app-specific runtime packages (empty for windowscross)
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

### Dockerfile Processing Methodology

Before extracting individual fields, walk through the entire Dockerfile top-to-bottom to understand its architecture. Every binary name, output path, and dependency is **explicitly written** in the Dockerfile and Makefile — do not guess or infer them from the image name alone.

#### Core Concept: Component-Based Extraction

Each Dockerfile may build multiple components (binaries), but **one spec is generated per component**. The prompt identifies which component to extract. Each component is treated independently — even if multiple components share the same Dockerfile and Makefile, they are separate specs with separate binaries, entrypoints, and dependencies.

#### Source Roles

| Source | Role | Use for |
| ------ | ---- | ------- |
| **Dockerfile** | Defines the actual build steps executed in CI. Contains the canonical `go build` commands, package paths, `COPY` instructions, and `ENTRYPOINT` declarations. | Binary names, output paths, build commands, package paths, entrypoints, runtime deps |
| **Makefile** | Defines variable values (`VERSION`, directory paths, ldflags formulas). Its build targets are for **local development** and may differ from the Dockerfile's CI build. | Resolving `$VAR` / `$(VAR)` references found in Dockerfile commands; directory variable definitions |
| **Target component** (from prompt) | Identifies which component this spec is for. The prompt provides this as the image/component name. | Selecting which binary to extract when the Dockerfile builds multiple components. **Not** the source of binary names or paths — those come from the Dockerfile and Makefile. |

#### Step-by-Step Stage Analysis

1. **Enumerate all stages.** Parse every `FROM ... AS <name>` to build a complete stage map.

2. **Classify each stage and document its role.** Every stage must be accounted for — do not skip any. Build a pipeline description that shows how artifacts flow from stage to stage:

   | Stage type | Recognition signal | Record |
   | ---------- | ------------------ | ------ |
   | **Base image** | `FROM <image> AS <name>` with no `RUN` build commands; provides toolchain or OS | Role: "provides Go compiler" / "provides OS tools" |
   | **Build stage** | Contains `RUN go build -o <path>` producing binaries from the repo's source code | Binary name, output path, package path, ldflags; list ALL `RUN go build` commands |
   | **Intermediate stage** | Copies from build stage; runs gathering, checksumming, or compression (`COPY`, `sha256sum`, `gzip`) | What it copies, what it produces (e.g. "gathers 5 binaries + config files into /payload, compresses them") |
   | **Wrapper stage** | Contains `go build` for a *different* Go module (separate `go.mod`, different ldflags module path); often downloads the module via `go mod download` | Binary name, module path, what it embeds (e.g. "embeds compressed payload from compressor stage into self-extracting binary") |
   | **Final stage** | `FROM scratch`, `FROM <base> AS linux`, `FROM <base> AS windows`; contains `ENTRYPOINT`/`CMD` | What binary it copies, from which stage, and its ENTRYPOINT |

   **You must produce a stage-by-stage pipeline summary** like:
   ```
   go          → base image providing Go 1.24 compiler
   mariner-core → base image providing gzip/sha256sum tools
   azure-vnet  → BUILD: compiles 5 CNI binaries (azure-vnet, azure-vnet-telemetry, azure-vnet-ipam, azure-vnet-stateless, azure-cni-telemetry-sidecar) from WORKDIR /azure-container-networking
   compressor  → INTERMEDIATE: gathers binaries + 6 config .conflist files + telemetry config into /payload, checksums, gzips
   dropgz      → WRAPPER: downloads dropgz module, embeds compressed /payload, builds self-extracting /go/bin/dropgz
   linux       → FINAL: COPY dropgz from wrapper → ENTRYPOINT ["/dropgz"]
   windows     → FINAL: COPY dropgz.exe from wrapper → ENTRYPOINT ["/dropgz.exe"]
   ```
   This pipeline summary ensures you understand how the Dockerfile works end-to-end before extracting any values.

3. **Identify the target binary** — work through these sub-steps in order:

   **3a. Inventory all binaries.** Walk through every build stage and every wrapper stage. For each `RUN go build -o <path>` command, record:
   - The binary name (from the `-o` flag's last path segment)
   - Which stage produced it
   - The full build command, ldflags, and package path

   **3b. Trace from the final stage backwards.** Read `ENTRYPOINT` and `COPY --from=<stage>` in the final stage(s) to see what binary ends up in the container.

   **3c. Determine if the final stage's binary comes from a build stage or a wrapper.**
   - If `COPY --from=<build-stage>` → the binary comes directly from a build stage. Emit its `go build` commands as `binaries[]`. No `pipelineSteps` needed.
   - If `COPY --from=<wrapper-stage>` → the final binary is a wrapper. **Proceed to 3d.**

   **3d. When the final stage uses a wrapper (compress→wrap pipeline):**
   The Dockerfile pipeline is: build-stage binaries → intermediate (gather + compress) → wrapper (embed + go build). The Dalec build steps must reproduce this **entire pipeline** 1:1.

   **What to emit:**
   1. **`binaries[]`** — one entry per `go build` in the build stage (these are the intermediate binaries fed into the compressor).
   2. **`pipelineSteps[]`** — ordered shell commands from the intermediate and wrapper stages:
      - File gathering (`mkdir -p`, `cp` commands from the compressor `COPY` instructions)
      - Checksumming and compression (`sha256sum`, `gzip`)
      - Module download for the wrapper (`go mod download ...@${VERSION}`)
      - Payload embedding (`cp /payload/* pkg/embed/fs/`)
      - Wrapper go build (`go build -o /go/bin/<wrapper> ...`)
   3. **Targets entrypoint** — use the **wrapper binary's name** (from the final stage's ENTRYPOINT), since that is what the container actually runs.

   **Why:** The wrapper IS the final artifact. The 5 build-stage binaries are intermediate inputs that get gathered, compressed, and embedded into the wrapper. Dalec must reproduce the entire pipeline to produce the correct final binary.

   **How to translate Docker `COPY` instructions to shell `cp` commands:**
   - `COPY --from=<stage> /go/bin/* /payload/` → `cp /go/bin/azure-vnet /go/bin/azure-vnet-telemetry ... /payload/` (list all binaries explicitly)
   - `COPY --from=<stage> /repo/path/file.conf /payload/name.conf` → `cp path/file.conf /payload/name.conf` (use repo-relative path)
   - Resolve `$OS` to `linux` (or `${TARGETOS}` if the pipeline should be cross-platform)

   **Example pipeline steps for a compress→wrap Dockerfile:**
   ```yaml
   pipelineSteps:
     - "mkdir -p /payload"
     - "cp /go/bin/binary-A /go/bin/binary-B /go/bin/binary-C /payload/"
     - "cp cni/azure-linux.conflist /payload/azure.conflist"
     - "cp cni/azure-linux-swift.conflist /payload/azure-swift.conflist"
     - "cp telemetry/azure-vnet-telemetry.config /payload/azure-vnet-telemetry.config"
     - "cd /payload && sha256sum * > sum.txt"
     - "gzip --verbose --best --recursive /payload && for f in /payload/*.gz; do mv -- \"$f\" \"${f%%.gz}\"; done"
     - "go mod download github.com/azure/azure-container-networking/dropgz@${DROPGZ_VERSION}"
     - "cd /go/pkg/mod/github.com/azure/azure-container-networking/dropgz@${DROPGZ_VERSION} && cp /payload/* pkg/embed/fs/ && go build -a -o /go/bin/dropgz -trimpath -ldflags \"-s -w -X github.com/Azure/azure-container-networking/dropgz/internal/buildinfo.Version=${VERSION}\" -gcflags=\"-dwarflocationlists=true\" main.go"
   ```

4. **Extract the build command.** From the identified stage's `RUN go build ...` line, extract:
   - `-o <outputPath>` → binary output path (rewrite to `/go/bin/<name>`)
   - `-ldflags "..."` → linker flags
   - Package path (e.g. `./cmd/main`, `.`, `cmd/service/*.go` → `.`)
   - Build flags (`-a`, `-trimpath`, `-gcflags`, etc.)

5. **Resolve variables.** For any `$VAR` or `$(VAR)` in the build command:
   - Look in the Makefile for the variable definition and substitute it.
   - Preserve `${VERSION}`, `${COMMIT}`, `${REVISION}` as Dalec spec args.
   - Convert `$(VAR)` Makefile syntax to `${VAR}` Dalec syntax.

6. **Determine targets.** Based on the final stage(s):
   - Linux final stage present → include `azlinux3/container`
   - Windows final stage present → include `windowscross/container`
   - Entrypoint per target: use the **final container binary's name**. For direct builds, this is the built binary. For wrapper pipelines, this is the **wrapper binary** (e.g. `dropgz`) since that is what the container ENTRYPOINT runs. Linux → full absolute path; Windows → bare binary name, no path prefix, no `.exe`.

7. **Target component usage summary.** The target component (provided in the prompt) identifies which Dockerfile pipeline to extract. For wrapper-pipeline Dockerfiles, ALL stages are relevant — emit all build-stage binaries in `binaries[]` and all intermediate/wrapper commands in `pipelineSteps[]`. The component name is never the source of binary names or paths — those always come from the Dockerfile.

#### Concrete Walkthrough

Consider a Dockerfile with these stages:

```
Stage 1 (build): FROM go AS my-app
  RUN go build -o /go/bin/my-service ... service/main.go
  RUN go build -o /go/bin/my-telemetry ... telemetry/main.go
  RUN go build -o /go/bin/my-ipam ... ipam/plugin/main.go

Stage 2 (intermediate): FROM base AS compressor
  COPY --from=my-app /go/bin/* /payload/
  COPY config files into /payload/
  RUN sha256sum, gzip everything

Stage 3 (wrapper): FROM go AS dropgz
  RUN go mod download ... dropgz module (separate go.mod)
  COPY --from=compressor /payload/* pkg/embed/fs/
  RUN go build -o /go/bin/dropgz ... main.go

Stage 4 (final linux): FROM scratch AS linux
  COPY --from=dropgz /go/bin/dropgz dropgz
  ENTRYPOINT ["/dropgz"]

Stage 5 (final windows): FROM hpc AS windows
  COPY --from=dropgz /go/bin/dropgz dropgz.exe
  ENTRYPOINT ["/dropgz.exe"]
```

**Step 1 – Enumerate:** 5 stages: `my-app`, `compressor`, `dropgz`, `linux`, `windows`.

**Step 2 – Classify and build pipeline summary:**
```
my-app     → BUILD: compiles 3 binaries (my-service, my-telemetry, my-ipam) from WORKDIR /repo, source code via COPY . .
compressor → INTERMEDIATE: copies all 3 binaries + config files into /payload, runs sha256sum, gzips everything
dropgz     → WRAPPER: downloads dropgz Go module (separate go.mod), embeds compressed /payload from compressor, builds self-extracting /go/bin/dropgz
linux      → FINAL: COPY dropgz from wrapper → ENTRYPOINT ["/dropgz"]
windows    → FINAL: COPY dropgz.exe from wrapper → ENTRYPOINT ["/dropgz.exe"]
```
Pipeline flow: source → my-app (compile) → compressor (gather + gzip) → dropgz (embed into self-extracting binary) → linux/windows (container with dropgz)

**Step 3 – Identify target binary:**
- 3b: Final stages both `COPY --from=dropgz` → binary comes from wrapper stage.
- 3c: Wrapper stage → proceed to 3d.
- 3d: Wrapper pipeline detected. Emit the FULL pipeline:
  - `binaries[]` = all 3 build-stage binaries (my-service, my-telemetry, my-ipam)
  - `pipelineSteps[]` = compressor + wrapper commands (gather, sha256sum, gzip, go mod download, embed, go build dropgz)
  - Final artifact = `/go/bin/dropgz` (the wrapper binary)

**Step 4 – Extract build commands:**
- `binaries[]`: 3 entries from the build stage's `RUN go build` lines
- `pipelineSteps[]`: translate compressor's `COPY` → `cp`, `RUN` → shell commands, wrapper's `RUN go mod download` + `RUN go build`

**Step 6 – Entrypoint:** Uses `dropgz` (the wrapper): Linux → `/dropgz`, Windows → `dropgz`.

**Example NonDeterministicValues output:**
```yaml
binaries:
  - name: "my-service"
    outputPath: "/go/bin/my-service"
    buildCommand: "go build -o /go/bin/my-service ... service/main.go"
    ldFlags: "..."
  - name: "my-telemetry"
    outputPath: "/go/bin/my-telemetry"
    buildCommand: "go build -o /go/bin/my-telemetry ... telemetry/main.go"
    ldFlags: "..."
  - name: "my-ipam"
    outputPath: "/go/bin/my-ipam"
    buildCommand: "go build -o /go/bin/my-ipam ... ipam/plugin/main.go"
    ldFlags: "..."

pipelineSteps:
  - "mkdir -p /payload"
  - "cp /go/bin/my-service /go/bin/my-telemetry /go/bin/my-ipam /payload/"
  - "cp path/to/config.conflist /payload/config.conflist"
  - "cd /payload && sha256sum * > sum.txt"
  - "gzip --verbose --best --recursive /payload && for f in /payload/*.gz; do mv -- \"$f\" \"${f%%.gz}\"; done"
  - "go mod download github.com/example/repo/dropgz@${DROPGZ_VERSION}"
  - "cd /go/pkg/mod/github.com/example/repo/dropgz@${DROPGZ_VERSION} && cp /payload/* pkg/embed/fs/ && go build -a -o /go/bin/dropgz ... main.go"

targets:
  - targetOS: "azlinux3/container"
    entrypoint: "/dropgz"
    symlink: "/usr/bin/dropgz"
    build: []
    runtime: []
  - targetOS: "windowscross/container"
    entrypoint: "dropgz"
    symlink: ""
    build: []
    runtime: []
```

---

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

#### 0.3 Multi-Image Dockerfiles

Some Dockerfiles produce **multiple images** from a single file. The Dockerfile defines several named build stages, each building a different component, plus shared stages (compressor, wrapper) that combine them.

**Recognition signals:**
- Multiple named build stages (e.g. `FROM ... AS build-foo`, `FROM ... AS build-bar`)
- Each stage has its own `go build` producing a distinctly-named binary
- Shared intermediate stages (compressor, packager) that copy outputs from build stages
- A wrapper stage that builds a packaging binary from a separate Go module
- Multiple final stages (`FROM scratch AS linux`, `FROM ... AS windows`)

**Rules:**
1. **One spec per component.** The pipeline generates one NonDeterministicValues per component. Extract ALL binaries from the build stage relevant to the target component — if the stage has multiple `RUN go build` commands, emit one Binary entry per command. The transformer merges them into a single command block.
2. **Trace from the final stage backwards.** Use `COPY --from=<stage>` and `ENTRYPOINT` in the final stage to identify which build stage produced the target binary. The build stage's `go build -o <path>` command provides the authoritative binary name and path. The component name from the prompt helps disambiguate but is not the source of truth for binary names.
3. **Document ALL stages in the pipeline** (see Step 2 in the methodology). Even though Dalec replaces intermediate and wrapper stages, you must understand what each stage does so you extract the correct binaries and config file dependencies. The intermediate and wrapper stages tell you what files the final image actually needs.
4. **See Rule 9 in Task 1** for how to emit the full pipeline when the Dockerfile uses a wrapper.

#### 0.4 Extraction Checklist

- [ ] **Pipeline summary written** — every `FROM ... AS <name>` stage classified and its role documented (Step 2)
- [ ] Check Makefile for `GOOS` references — does it build for both `linux` and `windows`?
- [ ] Check Dockerfile for platform-specific instructions and ENTRYPOINT/CMD per stage
- [ ] If this is a multi-component Dockerfile, trace from the final stage's `COPY --from`/`ENTRYPOINT` backwards to identify the correct build stage (use the target component name to disambiguate when all finals share a wrapper)
- [ ] For each target: determine entrypoint path, symlink, and any extra deps
- [ ] Only add rpm/deb targets if explicitly indicated in the build files
- [ ] `windowscross.runtime` must always be empty
- [ ] Do NOT emit: `msft-golang`, `SymCrypt`, `SymCrypt-OpenSSL`, `openssl-libs`

#### 0.5 Patterns

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

**Input:** Dockerfile and Makefile content provided in prompt

> **CRITICAL — Dockerfile-first rule:** Always check the Dockerfile **before** the Makefile for the actual `go build` command. The Dockerfile contains the canonical build command exactly as executed in CI. The Makefile is used for **variable definitions only** (e.g. `VERSION`, `LDFLAGS`, directory variables) — its build targets are for local development and may use patterns that differ from the CI build. If a `RUN go build` or `RUN GOOS=... go build` statement appears in the Dockerfile, that command is authoritative. Use the Makefile only to resolve variable values referenced in that command.
>
> **Source roles summarized:**
> - **Dockerfile** → build commands, binary names, output paths, package paths, entrypoints, dependencies
> - **Makefile** → variable values (`VERSION`, `*_DIR`, `*_PATH`, ldflags components). Its build steps are for local dev and less relevant.
> - **Target component** (from prompt) → identifies which component this spec is for. Not the source of binary names or paths.
>
> **Example:** The Dockerfile contains:
> ```dockerfile
> RUN GOOS=$OS CGO_ENABLED=0 go build -a -o /go/bin/myapp -ldflags "-s -w -X main.version=\"$VERSION\" -X \"$AI_PATH\"=\"$AI_ID\"" -gcflags="-dwarflocationlists=true" cmd/service/*.go
> ```
> The package path is `cmd/service/*.go`. This must be taken verbatim from the Dockerfile. The Makefile may reference `$(SERVICE_DIR)` or use a different invocation that omits this path — do not use the Makefile's version.

- Makefile content - Used to resolve variable values (e.g. `AI_PATH`, `AI_ID`, version vars, directory vars)
- Dockerfile content - **Primary source for the actual `go build` command, package path, and binary output**

**Output:** `Binaries` (YAML fields in output)

#### 1.1 Core Principle: Deterministic Build Steps

**CRITICAL:** Each binary extraction MUST produce a single, deterministic build step. The Dockerfile and Makefile contain all the information needed — there is no ambiguity. When a build stage has multiple `RUN go build` commands, emit one Binary entry per command — the transformer merges them all into a single `command:` block with one `BIN_SUFFIX` preamble and one `cd` at the top.

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
6. **Resolve Makefile directory variables to their actual defined value.** When the `-o` flag uses a Makefile variable for the output directory (e.g. `$(SERVICE_BUILD_DIR)`, `$(PLUGIN_BUILD_DIR)`), you MUST look up that variable's definition in the Makefile and substitute the literal string. Do NOT invent a path by using the binary name as the directory name — that is always wrong.
   - `SERVICE_BUILD_DIR := output/service` → `-o output/service/my-service` ✅
   - Guessing `output/my-service/my-service` because the binary is named `my-service` ❌
7. **Always set `outputPath` to `/go/bin/<binary-name>` and use the same path in `buildCommand -o`.** This path always exists in every Go builder image (it is `$GOPATH/bin`), requires no `mkdir`, and works correctly regardless of which subdir the build `cd`s into. Never use a relative path like `output/service/my-service` — the parent directory may not exist in the build sandbox.
8. **Single `command:` block — full pipeline merges.** Each binary's `buildCommand` is a single `go build` invocation (possibly prefixed with `cd <subdir> &&`). The transformer merges ALL binaries plus any `pipelineSteps` into **one** `command:` step with a single `BIN_SUFFIX` preamble, then the binary builds, then the pipeline steps. Emit each binary as a separate entry in the `binaries[]` array and each intermediate/wrapper command as a separate entry in `pipelineSteps[]`.

   **Filesystem alignment:** The Dalec build sandbox clones the repo into a directory named after the repo (e.g. `azure-container-networking/`). The `cd <repo-name>` at the top of the merged command places all builds at the repo root — exactly matching the Dockerfile's `WORKDIR`/`COPY . .` context. All `go build` package paths (e.g. `cni/network/plugin/main.go`) are relative to this root, just as they are in the Dockerfile. Pipeline steps that use absolute paths (e.g. `/payload`, `/go/pkg/mod/...`) work as-is.

   **WORKDIR → `cd` translation (CRITICAL):** When a Dockerfile build stage uses `WORKDIR <path>` to change directory before `RUN go build`, the `buildCommand` **MUST** include `cd <subdir> &&` as a prefix. The Dalec sandbox does NOT honour Dockerfile `WORKDIR` — the build script starts at the repo root, so missing the `cd` causes `go build` to run in the wrong directory (typically producing "no Go files" errors).
   - Strip the repo-name prefix from the WORKDIR path to get a repo-relative subdir. E.g. `WORKDIR /azure-container-networking/cns/service` → `cd cns/service &&`.
   - If the WORKDIR uses a variable (e.g. `WORKDIR /azure-container-networking/${CNS_DIR}`), **preserve the variable reference**: `cd ${CNS_DIR} &&`. The transformer promotes it to a Dalec arg.
   - If the WORKDIR is just the repo root (e.g. `WORKDIR /azure-container-networking`), no extra `cd` is needed — the transformer already `cd`s into the repo root.
   - When both a Makefile target `cd $(X_DIR)` and a Dockerfile `WORKDIR` exist, the Makefile `cd` takes precedence (it is more specific).

   **Example — WORKDIR before go build:**
   ```dockerfile
   # Dockerfile
   ARG CNS_DIR=cns/service
   WORKDIR /azure-container-networking/${CNS_DIR}
   RUN go build -v -o /go/bin/azure-cns -ldflags "-X main.version=${VERSION}" .
   ```
   ```yaml
   # ✅ CORRECT: cd prefix from WORKDIR
   buildCommand: "cd ${CNS_DIR} && go build -v -o /go/bin/azure-cns -ldflags \"-X main.version=${VERSION}\" ."
   ```
   ```yaml
   # ❌ WRONG: missing cd — go build runs at repo root, finds no Go files
   buildCommand: "go build -v -o /go/bin/azure-cns -ldflags \"-X main.version=${VERSION}\" ."
   ```

9. **Wrapper pipelines: emit ALL stages, not just the final binary.** When the Dockerfile has a build→compress→wrap pipeline, the build steps must reproduce the entire pipeline 1:1. Emit:
   - `binaries[]` — all `go build` commands from the primary build stage
   - `pipelineSteps[]` — intermediate (file gathering, compression) + wrapper (module download, embedding, go build) commands in order

   **How to identify this pattern:**
   - The Dockerfile has a distinct stage that runs `go mod download <module>@<version>` or sets `WORKDIR` to a different module path before building the final binary.
   - An intermediate stage copies build outputs, compresses them, and feeds them to the wrapper.
   - The final image `ENTRYPOINT` is the wrapper binary (e.g. `dropgz`), not a build-stage binary.

   **How to translate Dockerfile stages to `pipelineSteps`:**
   - `COPY --from=<stage> /go/bin/* /payload/` → `cp /go/bin/binary-A /go/bin/binary-B ... /payload/` (list binaries explicitly)
   - `COPY --from=<stage> /repo/path/file /payload/name` → `cp path/file /payload/name` (repo-relative path)
   - `RUN <command>` → the command verbatim (e.g. `sha256sum`, `gzip`)
   - `RUN go mod download <module>@<version>` → `go mod download <module>@${VERSION_VAR}`
   - `RUN GOOS=$OS go build -o /go/bin/<wrapper> ...` → `go build -o /go/bin/<wrapper> ...` (strip GOOS/CGO_ENABLED, the env section handles those)
   - Resolve `$OS` to `linux` for config file paths (e.g. `azure-$OS.conflist` → `azure-linux.conflist`)

10. **Multi-stage build→compress→wrap Dockerfiles: selecting direct binary vs. wrapper binary.** Many Dockerfiles use a shared pipeline where one stage builds several binaries, an intermediate stage compresses them, and a wrapper stage produces a single self-extracting binary. **All final stages route through the wrapper** — so tracing backwards from ENTRYPOINT always yields the wrapper. But Dalec replaces the entire compressor→wrapper pipeline, so the correct binary depends on which image is being built.

   **How to identify this pattern:**
   - A build stage produces one or more binaries via `RUN go build`.
   - A subsequent `compressor`/`packager` stage copies those binaries, checksums them, and gzips them.
   - A `wrapper` stage downloads a separate Go module, embeds the compressed payload, and builds a self-extracting binary.
   - **All** final image stages (`linux`, `windows`) copy from the wrapper stage only. There is no per-image final stage.

   **Decision tree (follows Step 3d in the Methodology):**

   ```
   All final stages route through a wrapper (build → compress → wrap pipeline).

   Emit the FULL pipeline:
     │
     ├─ binaries[] = ALL go build commands from the primary build stage
     │
     ├─ pipelineSteps[] = intermediate + wrapper commands in order:
     │   1. File gathering (mkdir, cp binaries + configs to /payload)
     │   2. Checksumming + compression (sha256sum, gzip)
     │   3. Wrapper module download (go mod download)
     │   4. Payload embedding (cp /payload/* into wrapper module)
     │   5. Wrapper go build (go build -o /go/bin/<wrapper>)
     │
     └─ Entrypoint = wrapper binary name (what the container actually runs)
   ```

   **Key rules:**
   - **Reproduce the entire pipeline 1:1.** Every stage in the Dockerfile that contributes to the final binary must be represented in the output.
   - **`binaries[]` = build-stage go builds.** All `go build` commands from the primary build stage go here.
   - **`pipelineSteps[]` = intermediate + wrapper commands.** File gathering, compression, module download, embedding, and wrapper build go here.
   - **Entrypoint = the wrapper.** Since the wrapper is the final binary, the entrypoint uses the wrapper's name.
   - **Artifact = the wrapper.** When `pipelineSteps` contains a `go build -o /go/bin/<wrapper>`, the transformer automatically uses that as the final artifact.

   **Example: build→compress→wrap pipeline (emit full pipeline)**

   Dockerfile stages:
   ```
   build:      go build -o /go/bin/my-plugin ...     ← builds my-plugin
               go build -o /go/bin/my-telemetry ...  ← builds telemetry sidecar
   compressor: copies my-plugin + telemetry + configs, gzips them
   wrapper:    go build -o /go/bin/wrapper ...        ← self-extracting wrapper
   linux:      COPY --from=wrapper → ENTRYPOINT ["/wrapper"]
   windows:    COPY --from=wrapper → ENTRYPOINT ["/wrapper.exe"]
   ```

   ```yaml
   # ✅ CORRECT: full pipeline — all stages represented
   binaries:
     - name: "my-plugin"
       outputPath: "/go/bin/my-plugin"
       buildCommand: "go build -v -o /go/bin/my-plugin -ldflags \"...\" -gcflags=\"...\""
       ldFlags: "..."
     - name: "my-telemetry"
       outputPath: "/go/bin/my-telemetry"
       buildCommand: "go build -v -o /go/bin/my-telemetry -ldflags \"...\" -gcflags=\"...\""
       ldFlags: "..."

   pipelineSteps:
     - "mkdir -p /payload"
     - "cp /go/bin/my-plugin /go/bin/my-telemetry /payload/"
     - "cp path/to/config.conflist /payload/config.conflist"
     - "cd /payload && sha256sum * > sum.txt"
     - "gzip --verbose --best --recursive /payload && for f in /payload/*.gz; do mv -- \"$f\" \"${f%%.gz}\"; done"
     - "go mod download example.com/repo/wrapper@${WRAPPER_VERSION}"
     - "cd /go/pkg/mod/example.com/repo/wrapper@${WRAPPER_VERSION} && cp /payload/* pkg/embed/fs/ && go build -a -o /go/bin/wrapper -trimpath -ldflags \"...\" -gcflags=\"...\" main.go"

   targets:
     - targetOS: "azlinux3/container"
       entrypoint: "/wrapper"
       symlink: "/usr/bin/wrapper"
       build: []
       runtime: []
     - targetOS: "windowscross/container"
       entrypoint: "wrapper"
       symlink: ""
       build: []
       runtime: []
   ```

   ```yaml
   # ❌ WRONG: only emitting build-stage binaries, ignoring compressor/wrapper
   binaries:
     - name: "my-plugin"
       outputPath: "/go/bin/my-plugin"
       buildCommand: "go build ..."
   # Missing the compressor and wrapper stages — output won't match Dockerfile pipeline
   ```

#### 1.2 Multi-Binary Makefile: Selecting the Correct Target

**Scenario:** A single Makefile defines many build targets, each producing a different binary (e.g. `my-service`, `my-plugin`, `my-agent`, `my-cli`). The pipeline generates **one spec per component**, so only the target matching the requested component is relevant.

**How to identify the correct target:**

1. **Match on Dockerfile ENTRYPOINT/COPY (strongest signal).** If the Dockerfile copies a specific binary into the final stage or sets it as the entrypoint, that is the target binary. The Makefile target producing that binary is the correct one.
2. **Match on `-o` output path.** The Makefile target whose `-o` flag produces a filename matching the Dockerfile's COPY destination or ENTRYPOINT binary is the right one.
3. **Match on target component name.** When the Dockerfile produces multiple components and the above signals are ambiguous, the target component name from the prompt helps narrow down. Find the Makefile target whose output binary name relates to that component. But the Dockerfile is always the authoritative source.
4. **Ignore all other targets.** Do NOT extract build commands for unrelated binaries. Only extract the one that produces the target component's binary.

**Rules:**
- Extract **only** the binary whose name matches the component being built.
- If the matching target has prerequisites (e.g. `bpf-lib`), note them but do not extract their build commands as separate binaries.
- The `ldflags`, `outputPath`, and `buildCommand` must come from the matched target only — not from a different target in the same Makefile.

#### 1.2.1 Multi-Subdir Repos: Finding the Most Specific Target

Many repos use a root Makefile that defines many `*-binary` targets and many `*_DIR` variables, each pointing to a different source subdirectory. Every image has exactly **one** most-relevant target, and therefore exactly **one** `cd <dir>` in its build command.

**How to identify the most specific target — ranked by signal strength:**

1. **Dockerfile ENTRYPOINT/COPY** *(strongest).* The binary name in the final stage `COPY` or `ENTRYPOINT` directly identifies the correct target. Find the `*-binary` Makefile target whose `-o` output matches that name.

2. **`-o` output path.** The target whose `-o` flag produces a filename matching the Dockerfile COPY destination is the right one.

3. **Target component name similarity.** Find the `*-binary` target whose name most closely matches the target component (from the prompt). Prefer exact matches over partial ones. This is a useful disambiguation signal when multiple targets have similar names.

4. **Comments above the target.** When multiple targets share similar names, read the comment on the line immediately above each target. Pick the one described as the "primary" or "main" binary for the image.

**Resolution steps:**

1. Read the Dockerfile's final stage `ENTRYPOINT`/`COPY` to identify the binary name (also note the target component name for disambiguation).
2. List all `*-binary` targets in the Makefile.
3. Apply the signals above (ENTRYPOINT match → `-o` path → name match → comment) to identify the single best target.
4. That target contains exactly one `cd $(X_DIR)` — identify which `*_DIR` variable it uses.
5. Keep the **variable name** (not the resolved path) in the buildCommand: `cd ${X_DIR}`.
   The transformer promotes it to a Dalec top-level arg automatically.
6. **Never use more than one `cd` per buildCommand.** One `cd` before `go build` is the entire subdir navigation.

**Example A: wrapper binary — Dockerfile `ENTRYPOINT` is the wrapper, not the primary binary**

The Makefile defines a `my-component-binary` target, but the Dockerfile's final image uses a wrapper (e.g. a self-extracting Go binary from a separate module) as the entrypoint. Per Rule 9, the full build→compress→wrap pipeline must be reproduced.

The Dockerfile pipeline:
- Build stage: builds `my-component` from the `my-component-binary` Makefile target
- Compressor stage: gathers binary + configs, checksums, gzips
- Wrapper stage: embeds compressed payload, builds `wrapper`

```yaml
# ✅ CORRECT: full pipeline with all stages
binaries:
  - name: "my-component"
    outputPath: "/go/bin/my-component"
    buildCommand: "cd ${MY_COMPONENT_DIR} && go build -a -o /go/bin/my-component ..."
    ldFlags: "..."

pipelineSteps:
  - "mkdir -p /payload"
  - "cp /go/bin/my-component /payload/"
  - "cp path/to/config /payload/config"
  - "cd /payload && sha256sum * > sum.txt"
  - "gzip --verbose --best --recursive /payload && for f in /payload/*.gz; do mv -- \"$f\" \"${f%%.gz}\"; done"
  - "go mod download example.com/repo/wrapper@${WRAPPER_VERSION}"
  - "cd /go/pkg/mod/example.com/repo/wrapper@${WRAPPER_VERSION} && cp /payload/* pkg/embed/fs/ && go build -a -o /go/bin/wrapper ... main.go"

targets:
  - targetOS: "azlinux3/container"
    entrypoint: "/wrapper"
    symlink: "/usr/bin/wrapper"
```

**Example B: comment-guided selection among similar targets**

```makefile
# Multiple targets share a common prefix:
my-plugin-binary:           # ← "# Build the main network plugin binary." comment above this
    cd $(PLUGIN_NET_DIR) && go build -a -o $(PLUGIN_BUILD_DIR)/my-plugin$(EXE_EXT) ...

my-plugin-ipam-binary:      # IPAM plugin only
    cd $(PLUGIN_IPAM_DIR) && go build ...

my-plugin-telemetry-binary: # Telemetry sidecar only
    cd $(PLUGIN_TELEMETRY_DIR) && go build ...
```

For an image whose Dockerfile ENTRYPOINT is `/my-plugin`, `my-plugin-binary` is the most specific match — its comment says it IS the main plugin binary. The others are support binaries.

```yaml
# ✅ CORRECT: primary plugin binary selected by Dockerfile ENTRYPOINT + comment signal
binaries:
  - name: "my-plugin"
    outputPath: "/go/bin/my-plugin"
    buildCommand: "cd ${PLUGIN_NET_DIR} && go build -a -o /go/bin/my-plugin -ldflags \"...\" ..."
```

```yaml
# ❌ WRONG: picking a support binary (telemetry, ipam plugin) instead of the primary one
binaries:
  - name: "my-plugin-telemetry"
    buildCommand: "cd ${PLUGIN_TELEMETRY_DIR} && go build ..."
```

**Rules:**
- **Always one `cd <dir>` per buildCommand.** Never chain two `cd` calls.
- Keep the `*_DIR` variable name in the command — do not resolve it to a literal path.
- If target names are ambiguous, comments and Dockerfile ENTRYPOINT are the deciding signals.
- Extract the single most specific/relatable target — do not extract prerequisites or sibling targets.

#### 1.3 Extraction Checklist

- [ ] **Check Dockerfile first** for a `RUN go build` command — this is the canonical build command
- [ ] **Check for `WORKDIR` before `RUN go build`** — if the build stage sets `WORKDIR <path>` before the build command, the `buildCommand` MUST include `cd <subdir> &&` as a prefix (see Rule 8 WORKDIR → cd translation). Strip the repo-name prefix; preserve any `${VAR}` references.
- [ ] Extract the package path (e.g. `cns/service/*.go`, `./cmd/main`, `.`) directly from the Dockerfile `RUN` instruction — do NOT infer it from the Makefile
- [ ] Use the Makefile only to resolve variable values referenced in the Dockerfile command (e.g. `$CNS_AI_PATH`, `$(VERSION)`)
- [ ] Identify which image/binary is being built (from image name or Dockerfile ENTRYPOINT)
- [ ] **Multi-stage check:** If the Dockerfile has build→compress→wrap stages, emit full pipeline: `binaries[]` for build-stage go builds + `pipelineSteps[]` for intermediate + wrapper commands
- [ ] **Multi-binary stage check:** If a single Dockerfile stage runs multiple `RUN go build` commands, extract ONLY the one whose output matches the final stage's `COPY --from`/`ENTRYPOINT`
- [ ] If the Makefile has multiple binary targets, select **only** the one matching the image name
- [ ] Find the `go build -o <path>` command in the matched Dockerfile `RUN` or Makefile target
- [ ] Extract binary name from `-o` flag path (last path segment)
- [ ] If no `-o` flag, infer from `./cmd/<name>` package path
- [ ] **Collapse conditional branches** — pick the single production build path
- [ ] **Resolve all Makefile directory variables** used in the `-o` flag — look up each variable's definition (e.g. `SERVICE_BUILD_DIR := output/service`) and substitute the literal value; never guess a directory name from the binary name
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

# Pattern D: Makefile directory variable — resolve, then rewrite to /go/bin/
# SERVICE_BUILD_DIR := output/service
# my-service-binary:
#   cd $(SERVICE_DIR) && go build -o $(SERVICE_BUILD_DIR)/my-service$(EXE_EXT) ...
#
# Always rewrite outputPath to /go/bin/<binaryname> regardless of Makefile paths:
# → binaries[0].name: "my-service"
# → binaries[0].outputPath: "/go/bin/my-service"
# → binaries[0].buildCommand: "cd ${SERVICE_DIR} && go build -a -o /go/bin/my-service -ldflags \"...\" ..."
#
# /go/bin/ always exists, absolute, works from any cd subdir.
# Artifact keys: /go/bin/my-service (global/Linux), /go/bin/my-service.exe (windowscross)
#
# ❌ WRONG: keeping the original relative path — output/service/ may not exist in build sandbox
# → binaries[0].outputPath: "output/service/my-service"
# → binaries[0].buildCommand: "cd ${SERVICE_DIR} && go build -o output/service/my-service ..."

# Pattern E: Dockerfile WORKDIR sets build directory (no Makefile cd)
# Dockerfile:
#   ARG CNS_DIR=cns/service
#   WORKDIR /azure-container-networking/${CNS_DIR}
#   RUN go build -v -o /go/bin/azure-cns -ldflags "-X main.version=${VERSION}" .
#
# WORKDIR → cd prefix; preserve the variable reference:
# → binaries[0].name: "azure-cns"
# → binaries[0].outputPath: "/go/bin/azure-cns"
# → binaries[0].buildCommand: "cd ${CNS_DIR} && go build -v -o /go/bin/azure-cns -ldflags \"-X main.version=${VERSION}\" ."
#
# ❌ WRONG: omitting cd — go build runs at repo root and finds no Go files
# → binaries[0].buildCommand: "go build -v -o /go/bin/azure-cns -ldflags \"-X main.version=${VERSION}\" ."
```

#### 1.5 Anti-Patterns (DO NOT produce these)

```yaml
# ❌ WRONG: Any platform variable remaining in outputPath or buildCommand -o path
outputPath: "_output/${ARCH}/blobplugin"
buildCommand: "go build -o bin/${GOOS}_${GOARCH}/kubelogin ..."

# ✅ CORRECT: Always /go/bin/<binary-name>
outputPath: "/go/bin/blobplugin"
outputPath: "/go/bin/kubelogin"
buildCommand: "go build -o /go/bin/kubelogin ..."

# ❌ WRONG: Directory variable guessed from binary name instead of resolved from Makefile definition
# Makefile has: SERVICE_BUILD_DIR := output/service
outputPath: "output/my-service/my-service"   # invented — my-service is the binary name, NOT the dir
buildCommand: "cd ${SERVICE_DIR} && go build -o output/my-service/my-service ..."

# ✅ CORRECT: Always rewrite to /go/bin/<binary-name>
outputPath: "/go/bin/my-service"
buildCommand: "cd ${SERVICE_DIR} && go build -o /go/bin/my-service ..."

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
  cd ${SERVICE_DIR} && go build -o output/service/my-service${BIN_SUFFIX} ...

# ❌ WRONG: Relative path — output/service/ may not exist in the build sandbox
outputPath: "output/service/my-service"
buildCommand: "cd ${SERVICE_DIR} && go build -a -o output/service/my-service -ldflags \"...\" ..."

# ✅ CORRECT: /go/bin/ path — always exists, no mkdir needed, works from any cd subdir
outputPath: "/go/bin/my-service"
buildCommand: "cd ${SERVICE_DIR} && go build -a -o /go/bin/my-service -ldflags \"...\" ..."

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
- **Linux targets** (`azlinux3/container`, etc.): entrypoint is the full absolute path (e.g. `/usr/local/bin/myapp`); symlink is typically `/usr/bin/<binary-name>`.
- **Windows targets** (`windowscross/container`): entrypoint is just the binary name with no path prefix (e.g. `myapp`); symlink should be empty string.

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

#### 3.4 Patterns

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

**Input:** Dockerfile and Makefile content provided in prompt  
**Output:** `BuildCommand`, `LdFlags` fields inside each `Binary` entry

These rules supplement Task 1's extraction process with additional translation details.

#### 4.1 Translation Rules

1. **Remove environment variable assignments** that Dalec manages: `CGO_ENABLED=`, `GOOS=`, `GOARCH=`, `GOARM=`, `OS=`, `ARCH=`. These are set in the Dalec spec's `build.env` section.
2. **Convert `$(VAR)` Makefile syntax to `${VAR}` Dalec syntax.** Version variables (`$(CNS_VERSION)`, `$(TAG)`, `$(IMAGE_VERSION)`, any `*_VERSION`/`*_TAG`) **must be replaced with `${VERSION}`**. Keep all other variable names as-is.
3. **Replace shell glob patterns (`*.go`) with Go package path `.`**. Dalec has no shell glob expansion. If the Dockerfile uses `go build ... path/to/*.go`, replace the glob:
   - After `cd <subdir>`: use `.` (current directory)
   - From repo root: use `./path/to/` (package directory, no glob)
   - **Never emit `*.go` in a `buildCommand`** — causes `malformed import path: invalid char '*'`
4. **Remove `docker run` wrappers.** Strip `docker run golang:... /bin/bash -c "..."` — extract only the inner `go build` command.

#### 4.2 Patterns

```makefile
# Input (with env vars, conditionals, platform paths):
CGO_ENABLED=${CGO_ENABLED} GOOS=linux GOARCH=$(ARCH) go build -a \
    -ldflags "-X ${PKG}/pkg/version.Ver=$(TAG) -s -w" \
    -o _output/${ARCH}/binary ./cmd/main

# Output (env stripped, $(TAG)→${VERSION}, path→/go/bin/):
# BuildCommand: "go build -a -ldflags \"-X ${PKG}/pkg/version.Ver=${VERSION} -s -w\" -o /go/bin/binary ./cmd/main"
# LdFlags: "-X ${PKG}/pkg/version.Ver=${VERSION} -s -w"
```

```dockerfile
# Input (Dockerfile with glob):
RUN cd cmd/service && go build -o /go/bin/my-service ... cmd/service/*.go

# Output (glob replaced with .):
# BuildCommand: "cd ${SERVICE_DIR} && go build -a -o /go/bin/my-service ... ."
```

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
│  Process: Parse Dockerfile stages into build steps              │
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
│  - targets.<os>.dependencies (from TargetSpec.Build/Runtime)    │
│  - build.steps (from Binary.BuildCommand + BIN_SUFFIX injection)│
│  CLI fills remaining deterministic fields (license, version)    │
│  Output: Final Dalec YAML spec (result/output.yml)              │
└─────────────────────────────────────────────────────────────────┘
```

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
```

Equivalent Go struct (for reference):

```go
NonDeterministicValues{
    Binaries: []Binary{
        {Name: "blobplugin", OutputPath: "/go/bin/blobplugin", BuildCommand: "go build -a ...", LdFlags: "..."},
        {Name: "blobfuse-proxy", OutputPath: "/go/bin/blobfuse-proxy", BuildCommand: "go build ..."},
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
}
```
