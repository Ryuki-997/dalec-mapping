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
