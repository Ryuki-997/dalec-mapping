# LIMITATIONS

Known limitations of the current dalec-mapping pipeline.

## 1. No support for patching / CVE revision on the same tag

The pipeline generates a spec from a fixed tag and commit hash. There is no
mechanism to apply a security patch or CVE fix on top of an already-generated
spec for the same version. A new tag or commit is required to trigger a
regeneration.

## 2. No support for additional GOEXPERIMENT values

Build flags such as `GOEXPERIMENT=jsonv2` (or any other experiment) are not
detected or forwarded into the generated spec. Projects that rely on
non-default Go experiments (e.g. `aquasecurity/trivy`) will fail to build
correctly.

## 3. Only Makefile and Dockerfile are recognized as build descriptors

The discovery step looks exclusively for `Makefile` and `Dockerfile`. Projects
that use alternative build systems — most notably **GoReleaser** — cannot be
onboarded because their build configuration is not parsed or understood.

## 4. Hardcoded operating system versions

Target OS versions (e.g. Mariner 2, Azure Linux 3) are hardcoded in the
pipeline. There is no dynamic detection or configuration to support new or
custom OS versions without a code change.

## 5. Go-only project support

Despite `CargoHomeGenerator` and `PipGenerator` existing in the type system,
the entire transformer pipeline exclusively generates `go build` commands and
`gomod` source generators. Rust, Python, or Node.js projects cannot produce
valid specs.

## 6. No support for build-time secrets or private dependencies

Only `GIT_AUTH_HEADER` on explicitly declared git sources is supported. If a
project runs `go mod download` against a different private repository not
listed as a submodule source, authentication will fail at build time.

## 7. No support for monorepo multiple Dockerfiles per component

Only one Dockerfile path and one Makefile path can be specified per component
in onboard.yml. Projects that build multiple binaries from separate
Dockerfiles (e.g. one for agent, one for init-container) cannot express that
in a single component entry.

## 8. Go toolchain version cannot be pinned to a specific patch or digest

`GoVersion()` strips the image tag to major.minor only (e.g.
`1.24-azurelinux3.0` → `1.24`) and emits `>= 1.24` as a version constraint.
There is no mechanism to pin to a specific patch version like `1.24.3` or a
specific image digest. Projects requiring an exact toolchain version will
receive a looser constraint than intended.
