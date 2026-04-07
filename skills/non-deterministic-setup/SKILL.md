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
    TargetOS   string   `yaml:"targetOS"`   // e.g. "azlinux3/image"
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

```bash
result/{repo-name}/NonDeterministicValues.yml
```

Where `{repo-name}` is the repository name (e.g., `kubelogin`, `blob-csi-driver`).

---

## Agent Extraction Tasks

### Dockerfile Processing Methodology

Before extracting individual fields, walk through the entire Dockerfile top-to-bottom to understand its architecture. Every binary name, output path, and dependency is **explicitly written** in the Dockerfile and Makefile — do not guess or infer them from the image name alone.

**ABSOLUTE RULE — Zero Inference on Build Commands:** Every `buildCommand`, `ldFlags`, and `pipelineSteps` entry must be a **literal 1:1 copy** of what appears in the Dockerfile. Do NOT add, remove, reorder, or infer any flags. Do NOT pull flags from the Makefile that are not already present in the Dockerfile's `RUN` line. The Makefile is used ONLY to resolve `$VAR` / `$(VAR)` references that the Dockerfile explicitly uses — it is NEVER a source of additional flags. If the Dockerfile says `-ldflags "-X main.version=${VERSION}"`, the output must say exactly that — not `-ldflags "-s -w -X main.version=${VERSION}"`.

**ABSOLUTE RULE — No Synthesized `&&` Chains:** Each `buildCommand` and each `pipelineSteps` entry must contain exactly **one** logical shell command. The LLM must NEVER chain separate commands with `&&`. If a `buildCommand` needs a `cd` prefix, use a newline (`\n`) to separate it from the build command — NOT `&&`. Each `pipelineSteps` entry = one command; split multi-command lines into separate array entries. The ONLY exception: if the Dockerfile's `RUN` line itself contains `&&` **verbatim** (e.g. `RUN gzip ... && for f in ...`), preserve that `&&` as-is — it is part of the original command, not an LLM-synthesized chain.

**ABSOLUTE RULE — No `go mod download` in `pipelineSteps`:** Submodule dependencies fetched via `go mod download <module>@<version>` are handled automatically as DALEC sources by the transformer. Do NOT emit `go mod download` lines in `pipelineSteps[]`. The transformer adds them to the spec's `sources:` section and makes them available at `$BUILD_ROOT/<sourceKey>`. Only emit the build/copy commands that use the downloaded module — not the download itself.

#### Core Concept: Component-Based Extraction

Each Dockerfile may build multiple components (binaries), but **one spec is generated per component**. The prompt identifies which component to extract. Each component is treated independently.

#### Source Roles

| Source | Role | Use for |
| ------ | ---- | ------- |
| **Dockerfile** | Canonical build steps as executed in CI — the ONLY source of build commands and flags | Binary names, output paths, build commands, flags, ldflags, package paths, entrypoints, runtime deps |
| **Makefile** | Variable definitions ONLY (`VERSION`, directory paths) — NEVER a source of build flags or ldflags | Resolving `$VAR` / `$(VAR)` references that the Dockerfile explicitly uses. Do NOT pull flags from Makefile into build commands |
| **Target component** (from prompt) | Identifies which component this spec is for | Selecting which binary to extract. **Not** the source of binary names or paths |

#### Step-by-Step Stage Analysis

1. **Enumerate all stages.** Parse every `FROM ... AS <name>` to build a complete stage map.

2. **Classify each stage and document its role:**

   | Stage type | Recognition signal | Record |
   | ---------- | ------------------ | ------ |
   | **Base image** | `FROM <image> AS <name>` with no `RUN` build commands | Role: "provides Go compiler" / "provides OS tools" |
   | **Build stage** | Contains `RUN go build -o <path>` | Binary name, output path, package path, ldflags |
   | **Intermediate stage** | Copies from build stage; runs gathering/compression | What it copies, what it produces |
   | **Wrapper stage** | `go build` for a *different* Go module (separate `go.mod`) | Binary name, module path, what it embeds |
   | **Final stage** | `FROM scratch` / `FROM <base> AS linux/windows`; has `ENTRYPOINT`/`CMD` | Binary copied, source stage, ENTRYPOINT |

   **You must produce a stage-by-stage pipeline summary** before extracting any values.

3. **Identify the target binary:**

   **3a.** Inventory all binaries — for each `RUN go build -o <path>`, record name, stage, command, ldflags, package path.

   **3b.** Trace from the final stage backwards via `ENTRYPOINT` and `COPY --from=<stage>`.

   **3c.** If `COPY --from=<build-stage>` → direct build. Emit `binaries[]`, no `pipelineSteps`.
   If `COPY --from=<wrapper-stage>` → wrapper pipeline. Proceed to 3d.

   **3d. Wrapper pipeline (build→compress→wrap):** Emit the **entire pipeline** 1:1. See **Rule 10** in Task 1.1 for full details. In summary:
   - `binaries[]` — one entry per `go build` in the build stage
   - `pipelineSteps[]` — ordered shell commands from intermediate + wrapper stages
   - Entrypoint — the **wrapper binary's name**

   **COPY → cp translation (checksum-critical):**
   The goal is to match the Dockerfile 1:1. **Copy the Dockerfile's intent exactly — do not expand, split, or restructure.**
   - **Wildcard copy** (`COPY --from=<stage> /go/bin/* /payload/`): Emit `cp /go/bin/* /payload/` — **preserve the wildcard**. Do NOT expand into individual binary names.
   - **Individual copy** (separate `COPY` lines per file): Emit separate `cp` commands per file.
   - **Config files** (`COPY --from=<stage> /repo/path/file.conf /payload/name.conf`): `cp path/file.conf /payload/name.conf` (repo-relative path)

   ```yaml
   # ❌ WRONG: expanding wildcard into explicit binary list
   - "cp /go/bin/binary-A /go/bin/binary-B /go/bin/binary-C /payload/"
   # ✅ CORRECT: preserving the Dockerfile's wildcard
   - "cp /go/bin/* /payload/"
   ```

4. **Extract the build command** from the `RUN go build ...` line:
   - `-o <outputPath>` → rewrite to `/go/bin/<name>`
   - `-ldflags "..."` → linker flags
   - Package path (e.g. `./cmd/main`, `.`)
   - Build flags (`-a`, `-trimpath`, `-gcflags`, etc.)

5. **Resolve variables.** For any `$VAR` or `$(VAR)`:
   - Look in the Makefile for the definition and substitute.
   - Preserve `${VERSION}`, `${COMMIT}`, `${REVISION}` as Dalec spec args.
   - Convert `$(VAR)` Makefile syntax to `${VAR}` Dalec syntax.

6. **Determine targets.** Linux final stage → `azlinux3/image`. Windows final stage → `windowscross/image`. Entrypoint per target: Linux → full absolute path; Windows → bare binary name, no path prefix, no `.exe`.

7. **Target component usage.** The component name from the prompt identifies which pipeline to extract. It is never the source of binary names or paths.

---

### Task 0: Build Targets Configuration

**Input:** Dockerfile and Makefile content provided in prompt

**Output:** `Targets` — a list of `TargetSpec` objects

Each `TargetSpec` groups: `targetOS`, `entrypoint`, `symlink`, `build`, `runtime`.

The transformer automatically adds these — do NOT emit them:

| Auto-added package | Target | Reason |
| ------------------ | ------ | ------ |
| `msft-golang` | all targets (build) | Go toolchain |
| `SymCrypt` | azlinux3 (build + runtime) | FIPS crypto lib |
| `SymCrypt-OpenSSL` | azlinux3 (build + runtime) | systemcrypto provider |
| `openssl-libs` | azlinux3 (build + runtime) | CGO link |

#### 0.1 Allowed Targets

| Target | Description |
| ------ | ----------- |
| `azlinux3/image` | Azure Linux 3 container image (primary Linux target) |
| `azlinux3/rpm` | Azure Linux 3 RPM package |
| `bookworm/deb` | Debian Bookworm deb package |
| `noble/deb` | Ubuntu Noble deb package |
| `jammy/deb` | Ubuntu Jammy deb package |
| `focal/deb` | Ubuntu Focal deb package |
| `bionic/deb` | Ubuntu Bionic deb package |
| `windowscross/image` | Windows cross-compiled container image |

#### 0.2 Selection Rules

1. **Default (cross-platform):** Both Linux and Windows (or unspecified) → emit `azlinux3/image` + `windowscross/image`.
2. **Windows-only:** Only Windows targets → emit **only** `windowscross/image`.
3. **Linux-only:** Only Linux targets → emit **only** `azlinux3/image`.
4. **Additional targets:** Add rpm/deb only if explicitly indicated in build files.
5. **When in doubt**, default to `azlinux3/image` + `windowscross/image`.

#### 0.3 Multi-Image Dockerfiles

Some Dockerfiles produce multiple images from a single file.

**Rules:**
1. **One spec per component.** Extract ALL binaries from the relevant build stage — one Binary entry per `go build` command.
2. **Trace from the final stage backwards.** `COPY --from=<stage>` and `ENTRYPOINT` identify the authoritative binary name and path.
3. **Document ALL stages in the pipeline** — intermediate and wrapper stages tell you what files the final image needs.
4. **See Rule 10 in Task 1** for wrapper pipeline handling.

#### 0.4 Extraction Checklist

- [ ] **Pipeline summary written** — every `FROM ... AS <name>` classified and documented
- [ ] Check Makefile for `GOOS` references — does it build for both `linux` and `windows`?
- [ ] Check Dockerfile for platform-specific instructions and ENTRYPOINT/CMD per stage
- [ ] If multi-component: trace from final stage backwards to identify correct build stage
- [ ] For each target: determine entrypoint path, symlink, and any extra deps
- [ ] `windowscross.runtime` must always be empty
- [ ] Do NOT emit: `msft-golang`, `SymCrypt`, `SymCrypt-OpenSSL`, `openssl-libs`
- [ ] **Flags verbatim check:** entrypoint path matches Dockerfile ENTRYPOINT exactly (no additions); every build flag is a 1:1 copy of the Dockerfile's `RUN` line (no flags inferred from Makefile)

#### 0.5 Patterns

```yaml
# Cross-platform (default):
targets:
  - targetOS: "azlinux3/image"
    entrypoint: "/usr/local/bin/myapp"   # from Dockerfile ENTRYPOINT
    symlink: "/usr/bin/myapp"
    build: []                            # app-specific only
    runtime:
      - "ca-certificates"
  - targetOS: "windowscross/image"
    entrypoint: "myapp"                  # bare name, NO .exe — transformer adds it
    symlink: ""
    build: []
    runtime: []                          # always empty
```

---

### Task 1: Binary Output Extraction

**Input:** Dockerfile and Makefile content provided in prompt

> **CRITICAL — Dockerfile-first rule:** The Dockerfile contains the canonical build command as executed in CI. The Makefile is used for **variable definitions only** — its build targets are for local development and may differ. If `RUN go build` appears in the Dockerfile, that command is authoritative.

**Output:** `Binaries` (YAML fields in output)

#### 1.1 Core Principle: Deterministic Build Steps

**CRITICAL:** Each binary extraction MUST produce a single, deterministic build step. When a build stage has multiple `RUN go build` commands, emit one Binary entry per command — the transformer merges them.

**Rules:**
1. **Collapse all conditionals into one path.** Pick the **primary production path** matching the Dockerfile's final COPY/ENTRYPOINT. Do NOT preserve conditionals.

2. **Resolve platform-specific output paths.** Strip `${OS}`, `${ARCH}`, `${GOOS}`, `${GOARCH}`, `${TARGETARCH}`, `$(GOOS)`, `$(GOARCH)`, `$(ARCH)`, `$(OS)` from paths. Collapse any `//` or trailing `/`.
   - `bin/${GOOS}_${GOARCH}/kubelogin` → `/go/bin/kubelogin`
   - `_output/${ARCH}/blobplugin` → `/go/bin/blobplugin`
   - **No `.exe` in `outputPath` or `name`** — transformer appends `.exe` for `windowscross/image` automatically.

3. **Preserve `${VERSION}`, `${COMMIT}`, `${REVISION}` variables.** These are Dalec spec args.

4. **STRICT 1:1 COPY — zero inference on build commands and flags.** The `buildCommand`, `ldFlags`, and every `pipelineSteps` entry must be a **character-for-character copy** of the Dockerfile's `RUN` lines. Do NOT add, remove, reorder, or infer ANY flag — not from the Makefile, not from convention, not from "best practice". The Makefile is used ONLY to resolve `$VAR` references that the Dockerfile explicitly contains. If a flag does not appear in the Dockerfile's `RUN go build` line, it MUST NOT appear in the output.

   Common hallucinations to avoid:
   - Adding `-s -w` to `-ldflags` (strips symbols/DWARF — changes the binary)
   - Adding `-a` or `-trimpath` when the Dockerfile doesn't have them
   - Merging Makefile LDFLAGS into the Dockerfile command when the Dockerfile never references `${LDFLAGS}`

   ```yaml
   # Dockerfile has: RUN go build -o /go/bin/azure-cns -ldflags "-X main.version=${VERSION}" .
   # ❌ WRONG: -s -w added by LLM (not in Dockerfile, not even in Makefile)
   buildCommand: "go build -o /go/bin/azure-cns -ldflags \"-s -w -X main.version=${VERSION}\" ."
   ldFlags: "-s -w -X main.version=${VERSION}"
   # ✅ CORRECT: exact copy of Dockerfile's RUN line
   buildCommand: "go build -o /go/bin/azure-cns -ldflags \"-X main.version=${VERSION}\" ."
   ldFlags: "-X main.version=${VERSION}"
   ```

5. **No double slashes.** After removing platform variables, ensure no `//` remains.

6. **`outputPath` is always `/go/bin/<binary-name>`.** The `buildCommand -o` flag must match exactly.

7. **Resolve Makefile directory variables** to their defined values. Do NOT invent paths from the binary name.

8. **Always set `outputPath` to `/go/bin/<binary-name>`.** This path always exists in the Go builder image, requires no `mkdir`, works from any `cd` subdir. Never use relative paths.

9. **Single `command:` block — full pipeline merges.** The transformer merges ALL binaries + `pipelineSteps` into one `command:` step. The merged command `cd`s into the repo root first, then runs each binary's `buildCommand` in sequence.

   **WORKDIR / `cd` translation (CRITICAL):** The Dalec sandbox does NOT honour Dockerfile `WORKDIR`. How to handle it depends on the build command structure. Check each case in order:

   **Case A — WORKDIR at repo root + package path in `go build` argument** (most common):
   If `WORKDIR` points to the repo root (e.g. `/azure-container-networking`) and the `go build` command already specifies a package path argument (e.g. `cns/service/*.go`), do NOT add any `cd` prefix. The package path tells `go build` where to find the source. Replace `subdir/*.go` with `./subdir/`.
   ```yaml
   # Dockerfile:
   #   WORKDIR /azure-container-networking
   #   RUN go build -a -o /go/bin/azure-cns ... cns/service/*.go
   # ❌ WRONG: decomposed package path into cd + go build (breaks: "cd: too many arguments")
   buildCommand: "cd cns/service\ngo build -a -o /go/bin/azure-cns ... ."
   # ❌ WRONG: smashed cd and go build on same line (no separator at all)
   buildCommand: "cd cns/service go build -a -o /go/bin/azure-cns ... ."
   # ✅ CORRECT: no cd, keep package path as build target argument, *.go → ./subdir/
   buildCommand: "go build -a -o /go/bin/azure-cns ... ./cns/service/"
   ```

   **Case B — Single-binary stage with WORKDIR at a subdirectory** (one `RUN go build` with a WORKDIR pointing to a subdirectory, and the build target is `.` or `*.go`):
   - Use a newline-separated `cd` + `go build`: `"cd <subdir>\ngo build ... ."` — NOT `&&`.
   - Strip the repo-name prefix. E.g. `WORKDIR /azure-container-networking/cns/service` → `cd cns/service\n`.
   - Preserve variable references: `WORKDIR /repo/${CNS_DIR}` → `cd ${CNS_DIR}\n`.
   - Makefile `cd $(X_DIR)` takes precedence over Dockerfile `WORKDIR`.

   **Case C — Multi-binary stage** (multiple `RUN go build` in the same stage, each building from a different subdirectory):
   - Do **NOT** put `cd <subdir>` in each binary's `buildCommand`. The transformer already `cd`s into the repo root.
   - Instead, use the **repo-relative package path** as the Go build target: `go build ... cni/network/plugin/main.go`.
   - Each binary's `buildCommand` runs from the repo root — the package path tells `go build` where to find the source.

   ```yaml
   # Case A — WORKDIR at repo root, package path in go build argument:
   # Dockerfile:
   #   WORKDIR /azure-container-networking
   #   RUN go build -a -o /go/bin/azure-cns ... cns/service/*.go
   # ❌ WRONG: decomposed package path into cd + go build
   buildCommand: "cd cns/service\ngo build -a -o /go/bin/azure-cns ... ."
   # ❌ WRONG: smashed cd and go build on same line
   buildCommand: "cd cns/service go build -a -o /go/bin/azure-cns ... ."
   # ✅ CORRECT: no cd, keep package path, *.go → ./subdir/
   buildCommand: "go build -a -o /go/bin/azure-cns ... ./cns/service/"

   # Case B — Single-binary stage with WORKDIR at subdir:
   # Dockerfile:
   #   WORKDIR /azure-container-networking/cns/service
   #   RUN go build -v -o /go/bin/azure-cns ... .
   # ❌ WRONG: missing cd — go build runs at repo root, finds no Go files
   buildCommand: "go build -v -o /go/bin/azure-cns -ldflags \"-X main.version=${VERSION}\" ."
   # ❌ WRONG: synthesized && chain — LLM must not create && joins
   buildCommand: "cd ${CNS_DIR} && go build -v -o /go/bin/azure-cns -ldflags \"-X main.version=${VERSION}\" ."
   # ✅ CORRECT: cd on separate line (newline-separated)
   buildCommand: "cd ${CNS_DIR}\ngo build -v -o /go/bin/azure-cns -ldflags \"-X main.version=${VERSION}\" ."

   # Case C — Multi-binary stage (all from repo root WORKDIR):
   # ❌ WRONG: per-binary cd — each cd is relative to $BUILD_ROOT, breaks merged script
   buildCommand: "cd cni/network/plugin\ngo build -a -o /go/bin/azure-vnet ... main.go"
   buildCommand: "cd cni/telemetry/service\ngo build -a -o /go/bin/azure-vnet-telemetry ... telemetrymain.go"
   # ✅ CORRECT: no cd, use repo-relative package path from repo root
   buildCommand: "go build -a -o /go/bin/azure-vnet ... cni/network/plugin/main.go"
   buildCommand: "go build -a -o /go/bin/azure-vnet-telemetry ... cni/telemetry/service/telemetrymain.go"
   ```

10. **Wrapper pipelines: emit the FULL build→compress→wrap pipeline.** When the Dockerfile has a build→compress→wrap pipeline (build-stage binaries → intermediate gather/compress → wrapper embed/build), reproduce the **entire pipeline** 1:1.

   **How to identify:** The Dockerfile has a stage that runs `go mod download <module>@<version>` or sets `WORKDIR` to a different module path. An intermediate stage copies build outputs, compresses them. The final `ENTRYPOINT` is the wrapper binary, not a build-stage binary.

   **What to emit:**
   - `binaries[]` — ONLY `go build` commands from the **primary build stage** (the stage that compiles the repo's own source code). The wrapper `go build` is **NEVER** in `binaries[]`.
   - `pipelineSteps[]` — intermediate + wrapper commands in order. **Each entry = one command (no `&&` chains).** `go mod download` is omitted (handled as a DALEC source):
     1. File gathering (`mkdir -p`, `cp` from COPY instructions)
     2. `cd` to working directory (separate entry from the command that follows)
     3. Checksumming (`sha256sum`), compression (`gzip`)
     4. `cd` to wrapper module path
     5. Payload embedding (`cp /payload/* pkg/embed/fs/`)
     6. Wrapper go build (`go build -o /go/bin/<wrapper>`) — this is a pipeline step, NOT a binary
   - Entrypoint = the wrapper binary name

   **CRITICAL — wrapper binary placement:** The wrapper `go build` (e.g. `go build -o /go/bin/dropgz ...`) belongs **exclusively in `pipelineSteps[]`** as the final step. Do NOT also add it to `binaries[]`. The transformer constructs the build script by first emitting all `binaries[]` commands (prefixed with `cd <repo-root>`), then appending `pipelineSteps[]` in order. If the wrapper build appears in `binaries[]`, the transformer will `cd` into the wrong path, producing a "no such file or directory" error.

   **COPY → cp rules (same as Step 3d):**
   - **Wildcard**: `COPY --from=<stage> /go/bin/* /payload/` → `cp /go/bin/* /payload/` — **preserve the wildcard**
   - **Individual**: separate `COPY` lines → separate `cp` commands
   - **Config files**: `COPY --from=<stage> /repo/path/file /payload/name` → `cp path/file /payload/name`
   - `RUN <cmd>` → verbatim. Strip `GOOS`/`CGO_ENABLED` env prefixes (Dalec manages those).
   - Resolve `$OS` to `linux` for config file paths.

   ```yaml
   # ✅ CORRECT: full pipeline — all stages represented, repo-relative package paths (no per-binary cd)
   binaries:
     - name: "my-plugin"
       outputPath: "/go/bin/my-plugin"
       buildCommand: "go build -v -o /go/bin/my-plugin -ldflags \"...\" -gcflags=\"...\" cni/network/plugin/main.go"
       ldFlags: "..."
     - name: "my-telemetry"
       outputPath: "/go/bin/my-telemetry"
       buildCommand: "go build -v -o /go/bin/my-telemetry -ldflags \"...\" -gcflags=\"...\" cni/telemetry/service/telemetrymain.go"
       ldFlags: "..."
   pipelineSteps:
     - "mkdir -p /payload"
     - "cp /go/bin/* /payload/"
     - "cp path/to/config.conflist /payload/config.conflist"
     - "cd /payload"
     - "sha256sum * > sum.txt"
     - "gzip --verbose --best --recursive /payload && for f in /payload/*.gz; do mv -- \"$f\" \"${f%%.gz}\"; done"
     - "cd /go/pkg/mod/example.com/repo/wrapper@${WRAPPER_VERSION}"
     - "cp /payload/* pkg/embed/fs/"
     - "go build -a -o /go/bin/wrapper -trimpath -ldflags \"...\" -gcflags=\"...\" main.go"
   targets:
     - targetOS: "azlinux3/image"
       entrypoint: "/wrapper"
       symlink: "/usr/bin/wrapper"
       build: []
       runtime: []
     - targetOS: "windowscross/image"
       entrypoint: "wrapper"
       symlink: ""
       build: []
       runtime: []
   ```

   ```yaml
   # ❌ WRONG: only build-stage binaries, missing compressor/wrapper stages
   binaries:
     - name: "my-plugin"
       buildCommand: "go build ..."
   # Output won't match Dockerfile pipeline
   ```

   ```yaml
   # ❌ WRONG: wrapper binary in binaries[] — causes "no such file or directory" at build time
   binaries:
     - name: "my-plugin"
       buildCommand: "go build -o /go/bin/my-plugin ... cni/network/plugin/main.go"
     - name: "dropgz"                      # ← wrapper does NOT belong here
       buildCommand: "cd dropgz\ngo build -o /go/bin/dropgz ... ."
   pipelineSteps:
     - "cd dropgz"
     - "go build -o /go/bin/dropgz ..."    # ← also here = duplicated
   # The transformer cd's into the repo root for binaries[], then emits "cd dropgz"
   # which becomes an invalid path. Wrapper builds go in pipelineSteps[] ONLY.
   ```

   ```yaml
   # ❌ WRONG: go mod download in pipelineSteps — transformer handles this as a DALEC source
   pipelineSteps:
     - "go mod download example.com/repo/wrapper@${WRAPPER_VERSION}"
     - "cd /go/pkg/mod/example.com/repo/wrapper@${WRAPPER_VERSION}"
   # ✅ CORRECT: omit go mod download, only emit the cd + build commands
   pipelineSteps:
     - "cd /go/pkg/mod/example.com/repo/wrapper@${WRAPPER_VERSION}"
     - "cp /payload/* pkg/embed/fs/"
     - "go build -a -o /go/bin/wrapper ..."
   ```

   ```yaml
   # ❌ WRONG: synthesized && chain in pipelineSteps
   pipelineSteps:
     - "cd /payload && sha256sum * > sum.txt"
     - "cd /go/pkg/mod/wrapper@${VER} && cp /payload/* pkg/embed/fs/ && go build ..."
   # ✅ CORRECT: each command is a separate entry
   pipelineSteps:
     - "cd /payload"
     - "sha256sum * > sum.txt"
     - "cd /go/pkg/mod/wrapper@${VER}"
     - "cp /payload/* pkg/embed/fs/"
     - "go build ..."
   ```

#### 1.2 Multi-Binary Makefile: Selecting the Correct Target

When a Makefile defines many build targets, select the one matching the requested component.

**Signal strength (strongest first):**
1. **Dockerfile ENTRYPOINT/COPY** — the binary in the final stage is the target.
2. **`-o` output path** — match filename to Dockerfile's COPY destination.
3. **Target component name** — disambiguate among similar Makefile targets.
4. **Ignore all other targets.** Extract only the matching binary.

**Rules:**
- Extract **only** the binary whose name matches the component being built.
- The `ldflags`, `outputPath`, and `buildCommand` must come from the matched target only.

#### 1.2.1 Multi-Subdir Repos: Finding the Most Specific Target

Many repos use a root Makefile with many `*-binary` targets and `*_DIR` variables. Each image has exactly **one** most-relevant target and one `cd <dir>`.

**Resolution steps:**
1. Read the Dockerfile's final stage `ENTRYPOINT`/`COPY` to identify the binary name.
2. List all `*-binary` targets in the Makefile.
3. Apply signals: ENTRYPOINT match → `-o` path → name match → comment above target.
4. Keep the **variable name** in buildCommand: `cd ${X_DIR}`.
5. **Never use more than one `cd` per buildCommand.**

**Rules:**
- Keep the `*_DIR` variable name — do not resolve it to a literal path.
- If target names are ambiguous, comments and Dockerfile ENTRYPOINT are the deciding signals.

#### 1.3 Extraction Checklist

- [ ] **Check Dockerfile first** for `RUN go build` — this is the canonical build command
- [ ] **Check for `WORKDIR` before `RUN go build`** — Case A (repo root + package path argument): NO `cd`, keep `./subdir/` as build target. Case B (WORKDIR at subdir): include `cd <subdir>` on a separate line (newline, NOT `&&`). Case C (multi-binary): use repo-relative package path instead
- [ ] Extract package path directly from Dockerfile `RUN` instruction — NOT from Makefile
- [ ] Use Makefile only to resolve variable values referenced in the Dockerfile command
- [ ] Identify which binary is being built (from Dockerfile ENTRYPOINT)
- [ ] **Multi-stage check:** build→compress→wrap → emit full pipeline (Rule 10)
- [ ] **Multi-binary check:** multiple `RUN go build` → extract only the one matching final stage
- [ ] If Makefile has multiple targets, select **only** the one matching the image name
- [ ] Extract binary name from `-o` flag path (last segment)
- [ ] **Collapse conditional branches** — single production path only
- [ ] **Resolve Makefile directory variables** — look up definitions, never guess from binary name
- [ ] **Remove platform variables** from output paths (`${OS}`, `${ARCH}`, etc.)
- [ ] **No double slashes** in resulting path
- [ ] **No `.exe`** in `name` or `outputPath`
- [ ] **`outputPath` = `/go/bin/<binary-name>`** — always this absolute path
- [ ] **`buildCommand -o` matches `outputPath`** exactly
- [ ] **Flags 1:1 copy:** every flag in `buildCommand` and `ldFlags` is a character-for-character copy of the Dockerfile's `RUN go build` line — nothing added (especially not `-s -w`), nothing removed, nothing reordered, nothing inferred from the Makefile
- [ ] **Wildcards preserved:** if Dockerfile uses `COPY ... /go/bin/* ...`, pipelineSteps uses `cp /go/bin/* ...` — not an expanded binary list
- [ ] **No per-binary `cd`:** if the build stage produces multiple binaries, each `buildCommand` uses a repo-relative package path — NOT `cd <subdir>\ngo build ... .`
- [ ] **No synthesized `&&`:** every `buildCommand` and `pipelineSteps` entry contains ONE command. `cd` is newline-separated in `buildCommand`, or a separate array entry in `pipelineSteps`. Only preserve `&&` if it exists verbatim in the Dockerfile's `RUN` line
- [ ] **No `go mod download` in `pipelineSteps`:** submodule downloads are handled as DALEC sources — omit them entirely
- [ ] **Wrapper NOT in `binaries[]`:** if there is a wrapper pipeline, the wrapper `go build` appears ONLY in `pipelineSteps[]` — never as a Binary entry

#### 1.4 Patterns & Anti-Patterns

```yaml
# Platform vars → /go/bin/
# ❌ outputPath: "_output/${ARCH}/blobplugin"
# ✅ outputPath: "/go/bin/blobplugin"

# .exe — transformer adds it
# ❌ name: "kubelogin.exe"
# ✅ name: "kubelogin"

# Relative path — may not exist in sandbox
# ❌ outputPath: "output/service/my-service"
# ✅ outputPath: "/go/bin/my-service"

# outputPath must match buildCommand -o
# ❌ outputPath: "bin/kubelogin" / buildCommand: "go build -o bin/${OS}_${ARCH}/kubelogin ..."
# ✅ outputPath: "/go/bin/kubelogin" / buildCommand: "go build -o /go/bin/kubelogin ..."

# Double slashes after collapsing platform vars
# ❌ outputPath: "bin//kubelogin"
# ✅ outputPath: "/go/bin/kubelogin"

# Conditional preserved in build command
# ❌ buildCommand: "if [ \"$ARCH\" = \"amd64\" ]; then go build ... fi"
# ✅ buildCommand: "go build -o /go/bin/binary ./cmd/main"

# Flags added by LLM (not in Dockerfile — even if Makefile has them, do NOT add)
# ❌ buildCommand: "go build -o /go/bin/app -ldflags \"-s -w -X main.version=${VERSION}\" ."  (Dockerfile has no -s -w)
# ✅ buildCommand: "go build -o /go/bin/app -ldflags \"-X main.version=${VERSION}\" ."

# Wildcard expanded into binary list
# ❌ pipelineSteps: ["cp /go/bin/svc-A /go/bin/svc-B /go/bin/svc-C /payload/"]  (Dockerfile uses /go/bin/*)
# ✅ pipelineSteps: ["cp /go/bin/* /payload/"]

# Single-binary stage: missing cd from WORKDIR
# ❌ buildCommand: "go build -o /go/bin/azure-cns ."  (WORKDIR set subdir before RUN)
# ❌ buildCommand: "cd ${CNS_DIR} && go build -o /go/bin/azure-cns ."  (synthesized && — LLM must not create && joins)
# ✅ buildCommand: "cd ${CNS_DIR}\ngo build -o /go/bin/azure-cns ."  (newline-separated)

# Package path decomposed into cd (WORKDIR at repo root, go build has subdir/*.go)
# ❌ buildCommand: "cd cns/service\ngo build -a -o /go/bin/azure-cns ... ."  (split package path into cd + .)
# ❌ buildCommand: "cd cns/service go build -a -o /go/bin/azure-cns ... ."  (smashed together — no separator)
# ✅ buildCommand: "go build -a -o /go/bin/azure-cns ... ./cns/service/"  (no cd, keep package path as argument)

# Multi-binary stage: per-binary cd instead of package path
# ❌ buildCommand: "cd cni/network/plugin\ngo build -o /go/bin/azure-vnet ... main.go"  (per-binary cd breaks merged script)
# ✅ buildCommand: "go build -o /go/bin/azure-vnet ... cni/network/plugin/main.go"

# Makefile dir variable — resolve from definition, not from binary name
# Makefile: SERVICE_BUILD_DIR := output/service
# ❌ buildCommand: "cd ${SERVICE_DIR}\ngo build -o output/my-service/my-service ..."  (invented path)
# ✅ buildCommand: "cd ${SERVICE_DIR}\ngo build -a -o /go/bin/my-service ..."

# Synthesized && in pipelineSteps
# ❌ pipelineSteps: ["cd /payload && sha256sum * > sum.txt"]  (synthesized && chain)
# ✅ pipelineSteps: ["cd /payload", "sha256sum * > sum.txt"]  (separate entries)

# Verbatim && from Dockerfile RUN line — preserve as-is
# ✅ pipelineSteps: ["gzip --best /payload && for f in /payload/*.gz; do mv \"$f\" \"${f%%.gz}\"; done"]  (verbatim from Dockerfile)

# go mod download in pipelineSteps
# ❌ pipelineSteps: ["go mod download example.com/wrapper@${VER}"]  (handled as DALEC source)
# ✅ omit go mod download entirely — transformer adds it to sources
```

---

### Task 2: Entrypoint & Symlink

**Input:** Dockerfile content provided in prompt
**Output:** `entrypoint` and `symlink` fields **inside each TargetSpec** (not as top-level fields)

- **Linux targets**: entrypoint = full absolute path (e.g. `/usr/local/bin/myapp`); symlink = `/usr/bin/<binary-name>`.
- **Windows targets**: entrypoint = bare binary name (e.g. `myapp`); symlink = `""`.

#### 2.1 Extraction Checklist

- [ ] Find last `ENTRYPOINT` or `CMD` in final Dockerfile stage
- [ ] Parse command: array form `["/bin"]` or shell form `/bin`
- [ ] Extract executable path (first element if array)
- [ ] Derive symlink: `/usr/bin/<binary-name>` → `<entrypoint>`
- [ ] Verify entrypoint matches extracted binary name

#### 2.2 Patterns

```dockerfile
ENTRYPOINT ["/blobplugin"]        # → Entrypoint: "/blobplugin", Symlink: "/usr/bin/blobplugin"
CMD /pod_nanny                    # → Entrypoint: "/pod_nanny", Symlink: "/usr/bin/pod_nanny"
ENTRYPOINT ["/app", "--config"]   # → Entrypoint: "/app", Symlink: "/usr/bin/app"
```

---

### Task 3: Dependencies Extraction

**Input:** Dockerfile and Makefile content provided in prompt
**Output:** `build` and `runtime` fields **inside each TargetSpec**

#### 3.0 What the Transformer Already Provides (Do NOT emit)

| Package | Target | Why auto-added |
| ------- | ------ | -------------- |
| `msft-golang` | all targets (build) | Go toolchain |
| `SymCrypt` | azlinux3 (build + runtime) | FIPS crypto lib |
| `SymCrypt-OpenSSL` | azlinux3 (build + runtime) | systemcrypto provider |
| `openssl-libs` | azlinux3 (build + runtime) | OpenSSL shared libs |

Only emit **application-specific** packages.

#### 3.1 Structure

```yaml
targets:
  - targetOS: "azlinux3/image"
    build:
      - "curl"              # app-specific compile-time
    runtime:
      - "ca-certificates"   # app-specific runtime
  - targetOS: "windowscross/image"
    build: []
    runtime: []              # ALWAYS empty — Dalec rejects runtime deps on Windows
```

#### 3.2 Build Dependencies Checklist

- [ ] Check Dockerfile builder stage for `apt install` / `RUN` install commands
- [ ] Check Makefile for extra build tools
- [ ] Do NOT emit: `msft-golang`, `gcc`, `SymCrypt`, `SymCrypt-OpenSSL`, `openssl-libs`

#### 3.3 Runtime Dependencies Checklist

- [ ] Analyze **final Dockerfile stage only**
- [ ] Find `apt install`, `yum install`, `apk add`, `tdnf install` commands
- [ ] Exclude: shell syntax (`fi`, `then`), operators (`&&`), commands (`install`, `apt`), paths, variables, auto-added packages
- [ ] **Never populate `windowscross.runtime`**

#### 3.4 Patterns

```dockerfile
# Builder stage
FROM golang:1.21 AS builder
RUN apt install -y curl

# Final Linux stage
RUN apt install -y ca-certificates iptables
ENTRYPOINT ["/usr/local/bin/myapp"]

# → targets:
#   - targetOS: "azlinux3/image"
#     build: ["curl"]
#     runtime: ["ca-certificates", "iptables"]
#   - targetOS: "windowscross/image"
#     build: []
#     runtime: []    # always empty
```

```yaml
# ❌ WRONG: old perTargetDeps map format
perTargetDeps:
  azlinux3:
    runtime: ["iptables"]

# ✅ CORRECT: per-target fields inside each TargetSpec
targets:
  - targetOS: "azlinux3/image"
    runtime: ["iptables"]
```

---

### Task 4: Build Command Translation

**Input:** Dockerfile and Makefile content provided in prompt
**Output:** `BuildCommand`, `LdFlags` fields inside each `Binary` entry

#### 4.1 Translation Rules

1. **Remove environment variable assignments** that Dalec manages: `CGO_ENABLED=`, `GOOS=`, `GOARCH=`, `GOARM=`, `OS=`, `ARCH=`.
2. **Convert `$(VAR)` to `${VAR}`.** Version variables (`$(CNS_VERSION)`, `$(TAG)`, `$(IMAGE_VERSION)`, any `*_VERSION`/`*_TAG`) → **`${VERSION}`**. Keep all other variable names as-is.
3. **Replace shell glob `*.go` with Go package path.** The glob is always a build target argument — never split it into a `cd` + `.`.
   - After explicit `cd <subdir>`: replace `*.go` with `.`
   - From repo root (no `cd`): replace `path/to/subdir/*.go` with `./path/to/subdir/` — keep the full relative path as the build target argument
   - **Never emit `*.go`.** **Never decompose `subdir/*.go` into `cd subdir` + `.`.**
4. **Remove `docker run` wrappers.** Extract only the inner `go build` command.

---

## Self-Check Before Output

Before returning the NonDeterministicValues YAML, verify each item:

**Flags & Fidelity (1:1 copy — zero inference):**
- [ ] Compare every flag in each `buildCommand` against the Dockerfile's `RUN go build` line CHARACTER BY CHARACTER — are they identical?
- [ ] If the Dockerfile's `-ldflags` does NOT contain `-s -w`, confirm your output does NOT contain `-s -w` (even if the Makefile has them — the Makefile is NOT a source of flags)
- [ ] If the Dockerfile's `-ldflags` DOES contain `-s -w`, confirm your output DOES contain `-s -w`
- [ ] Are `-a`, `-trimpath`, `-v`, `-gcflags`, `-tags` present/absent exactly as in the Dockerfile? (NOT inferred from Makefile or convention)
- [ ] Did you add ANY flag that is not literally written in the Dockerfile's `RUN` line? If yes, REMOVE IT

**Paths:**
- [ ] Every `outputPath` is `/go/bin/<binary-name>` — no relative paths, no platform variables
- [ ] Every `buildCommand -o` path matches its `outputPath` exactly
- [ ] No `.exe` in any `name` or `outputPath`
- [ ] No `//` anywhere in paths

**Wildcards & COPY:**
- [ ] If the Dockerfile uses `COPY --from=... /go/bin/* ...`, the corresponding `pipelineSteps` entry uses `cp /go/bin/* ...` — NOT an expanded binary list
- [ ] Each Docker `COPY` → `cp` translation matches the Dockerfile's intent 1:1

**WORKDIR / cd:**
- [ ] WORKDIR at repo root + `go build ... subdir/*.go`: NO `cd` needed — keep `./subdir/` as the build target argument (Case A)
- [ ] Single-binary stage with `WORKDIR <subdir>`: `buildCommand` includes `cd <subdir>` on a separate line (newline, NOT `&&`) (Case B)
- [ ] Multi-binary stage: NO per-binary `cd` — each `buildCommand` uses repo-relative package path (e.g. `cni/network/plugin/main.go`) (Case C)
- [ ] No synthesized `&&` chains anywhere — each command is a separate line or array entry. Only preserve `&&` that exists verbatim in the Dockerfile's `RUN` line

**Pipeline Completeness:**
- [ ] If final stage copies from a wrapper → `binaries[]` has ALL build-stage go builds AND `pipelineSteps[]` has ALL intermediate + wrapper commands
- [ ] If final stage copies directly from build stage → no `pipelineSteps` needed
- [ ] Wrapper `go build` appears ONLY in `pipelineSteps[]` — never duplicated into `binaries[]`
- [ ] No `go mod download` in `pipelineSteps[]` — these are handled as DALEC sources automatically
- [ ] Each `pipelineSteps` entry is exactly ONE command — no `&&` chains (unless verbatim from Dockerfile)

**Variables:**
- [ ] `${VERSION}`, `${COMMIT}`, `${REVISION}` preserved as-is (not resolved)
- [ ] `$(VAR)` Makefile syntax converted to `${VAR}`
- [ ] `CGO_ENABLED`, `GOOS`, `GOARCH` env prefixes stripped from build commands

**Targets:**
- [ ] `windowscross.runtime` is empty
- [ ] Auto-added packages (`msft-golang`, `SymCrypt`, `SymCrypt-OpenSSL`, `openssl-libs`) are NOT in any target's deps

---

## Example Extraction

### Input Files

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

### Extracted Values

```yaml
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
  - targetOS: "azlinux3/image"
    entrypoint: "/blobplugin"
    symlink: "/usr/bin/blobplugin"
    build:
      - "curl"
    runtime:
      - "ca-certificates"
      - "fuse"
  - targetOS: "windowscross/image"
    entrypoint: "blobplugin"
    symlink: ""
    build: []
    runtime: []
```
