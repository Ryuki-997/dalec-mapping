package transformer

// ═══════════════════════════════════════════════════════════════════════════════
// extractTargets.go — Generates the `targets:` section of a Dalec spec.
//
//   Chunk 1 · ORCHESTRATION            extractTargetsSection(), buildTargetEntry()
//     Deduplicates OS names from BuildTargets, dispatches to per-OS builders.
//     buildTargetEntry routes Linux vs Windows assembly.
//     Calls → resolveTestPaths(), linuxDeps/ImageConfig, windowsDeps/Artifacts/ImageConfig
//
//   Chunk 2 · TEST PATHS               testPaths, resolveTestPaths()
//     Determines the binary name, install path, and symlink path used in
//     per-target file-existence tests.
//     Calls → canonicalBase()
//
//   Chunk 3 · LINUX TARGETS            linuxDeps(), linuxImageConfig()
//     Dependencies, image config, and extra repos for AZLinux / Debian / Ubuntu.
//     Calls → msftLinuxExtraRepo(), extractLinuxSymlinks()
//
//   Chunk 4 · WINDOWS TARGET           windowsDeps(), windowsArtifacts(), windowsImageConfig()
//     Dependencies, artifacts, and image config for the windowscross target.
//     Calls → computeWindowsArtifactBinaries(), extractWindowsSymlinks()
//
//   Chunk 5 · EXTRA REPOS              msftLinuxExtraRepo(), microsoftRepoURI(),
//                                       msftWindowsExtraRepo()
//     Microsoft package repository definitions for apt sources.
//
//   Chunk 6 · UTILITIES                findPrimaryLinuxTarget(),
//                                       entrypointBinaryName(), canonicalBase()
//     Target lookup helpers and shared functions used by other extract* files.
// ═══════════════════════════════════════════════════════════════════════════════

import (
	"dalec-mapping/domain/contents"
	"dalec-mapping/domain/repository"
	"dalec-mapping/infrastructure/parser"
	infraRepo "dalec-mapping/infrastructure/repository"
	"dalec-mapping/infrastructure/test"
	"dalec-mapping/pipeline"
	"log"

	"fmt"
	"strings"
)

// ─── Chunk 1 · ORCHESTRATION ─────────────────────────────────────────────────

// extractTargetsSection builds the `targets:` map for every build target in the spec.
// Each unique OS (azlinux3, windowscross, jammy, …) gets exactly one entry.
func extractTargetsSection() map[string]interface{} {
	// Analyse Dockerfile stages for intermediate runtime deps and final Linux base.
	intermediateDeps := parser.ExtractIntermediateRuntimeDeps(pipeline.Current.Dockerfile.Stages)
	finalLinuxBase := parser.DetectFinalLinuxBase(pipeline.Current.Dockerfile.Stages)

	targets := make(map[string]interface{})
	seen := make(map[string]bool)

	for _, buildTarget := range onboardBuildTargets() {
		osName := buildTarget.OS()
		if seen[osName] {
			continue
		}
		seen[osName] = true
		isContainer := buildTarget.IsContainer()
		tp := resolveTestPaths(osName, isContainer)
		targets[osName] = buildTargetEntry(osName, isContainer, tp, intermediateDeps, finalLinuxBase)
	}
	return targets
}

// buildTargetEntry assembles the full target map for one OS.
func buildTargetEntry(osName string, isContainer bool, tp testPaths, intermediateDeps []parser.IntermediateRuntimeDeps, finalLinuxBase string) map[string]interface{} {
	target := make(map[string]interface{})

	if osName == "windowscross" {
		target["dependencies"] = windowsDeps()
		target["artifacts"] = windowsArtifacts()
		target["image"] = windowsImageConfig(tp.binaryName)
	} else {
		target["dependencies"] = linuxDeps(osName, intermediateDeps)
		if isContainer {
			target["image"] = linuxImageConfig(tp.binaryName, finalLinuxBase)
		}
	}

	if isContainer {
		target["tests"] = []interface{}{
			test.TestCheckFiles(osName, tp.binaryName, tp.binaryPath, tp.symlinkPath, 0755),
		}
	}
	return target
}

// ─── Chunk 2 · TEST PATHS ───────────────────────────────────────────────────

// testPaths holds the resolved binary paths used for per-target file tests.
type testPaths struct {
	binaryName  string
	binaryPath  string // real installed binary (has permissions check)
	symlinkPath string // symlink pointing to binaryPath (existence-only check)
}

// resolveTestPaths determines the binary name and install paths used in file tests.
//
// For container targets: binaryPath = /usr/bin/<name> (real binary, permissions check),
//
//	symlinkPath = /usr/local/bin/<name> (symlink, existence-only)
//
// For package targets (deb/rpm): binaryPath = /usr/bin/<name>, symlinkPath = "" (no symlink)
func resolveTestPaths(osName string, isContainer bool) testPaths {
	onboard := pipeline.Current.Onboard

	tp := testPaths{
		binaryName: onboard.SpecImageName,
		binaryPath: "/usr/bin/" + onboard.SpecImageName,
	}
	if isContainer {
		tp.symlinkPath = "/usr/local/bin/" + onboard.SpecImageName
	}

	// Derive the binary name from the parsed artifact paths (same source used
	// by extractArtifacts). computeArtifactPaths reads pipeline.Current.Spec which is
	// populated by the Dockerfile AST parser.
	for artifactPath := range computeArtifactPaths() {
		if base := canonicalBase(artifactPath); base != "" {
			tp.binaryName = base
			if osName == "windowscross" {
				tp.binaryPath = "/Windows/System32/" + base + ".exe"
				tp.symlinkPath = ""
			} else {
				tp.binaryPath = "/usr/bin/" + base
				if isContainer {
					tp.symlinkPath = "/usr/local/bin/" + base
				}
			}
		}
		break
	}
	return tp
}

// ─── Chunk 3 · LINUX TARGETS ────────────────────────────────────────────────

// linuxDeps builds the dependencies map for a Linux target.
func linuxDeps(osName string, intermediateDeps []parser.IntermediateRuntimeDeps) map[string]interface{} {
	repoInfo := pipeline.Current.RepoInfo

	buildDeps := map[string]interface{}{}
	runtimeDeps := map[string]interface{}{}

	// ADO repos need git at build time for source fetching.
	if infraRepo.IsADORepo(repoInfo.GitURL) {
		buildDeps["git"] = map[string]interface{}{}
		if repoInfo.Generator == repository.GoModGenerator {
			buildDeps["msft-golang"] = goToolchainDep(pipeline.Current.RepoInfo.GoVersion)
		}
	}

	switch osName {
	case "azlinux3":
		if repoInfo.Generator == repository.GoModGenerator {
			buildDeps["msft-golang"] = goToolchainDep(pipeline.Current.RepoInfo.GoVersion)
		}
		for _, pkg := range []string{"SymCrypt", "SymCrypt-OpenSSL", "openssl-libs"} {
			buildDeps[pkg] = map[string]interface{}{}
			runtimeDeps[pkg] = map[string]interface{}{}
		}

	case "bookworm", "bullseye", "noble", "jammy", "focal", "bionic":
		if repoInfo.Generator == repository.GoModGenerator {
			buildDeps["msft-golang"] = goToolchainDep(pipeline.Current.RepoInfo.GoVersion)
			buildDeps["gcc"] = map[string]interface{}{}
		}
	}

	// Merge runtime deps extracted from Dockerfile intermediate stages.
	for _, idep := range intermediateDeps {
		for _, pkg := range idep.Packages {
			if _, exists := runtimeDeps[pkg]; !exists {
				runtimeDeps[pkg] = map[string]interface{}{}
				if idep.SelectiveCopy {
					log.Printf("⚠️  Runtime dep %q from stage %q: Dockerfile selectively copies files — full package will be installed by Dalec.\n", pkg, idep.StageName)
				}
			}
		}
	}

	deps := map[string]interface{}{}
	if len(buildDeps) > 0 {
		deps["build"] = buildDeps
	}
	if len(runtimeDeps) > 0 {
		deps["runtime"] = runtimeDeps
	}
	switch osName {
	case "bookworm", "bullseye", "noble", "jammy", "focal", "bionic":
		deps["extra_repos"] = []map[string]interface{}{msftLinuxExtraRepo(osName)}
	}
	return deps
}

// linuxImageConfig builds the image map (entrypoint + symlinks + optional base) for a Linux target.
// When finalLinuxBase is provided, it is emitted as a single image.bases entry.
func linuxImageConfig(binaryName, finalLinuxBase string) map[string]interface{} {
	entrypoint := "/usr/local/bin/" + binaryName
	symlink := "/usr/bin/" + binaryName

	image := map[string]interface{}{"entrypoint": entrypoint}

	// Add base image when detected from Dockerfile's final Linux stage.
	if finalLinuxBase != "" {
		image["bases"] = []map[string]interface{}{
			{
				"rootfs": map[string]interface{}{
					"image": map[string]interface{}{
						"ref": finalLinuxBase,
					},
				},
			},
		}
	}

	if symlink != "" {
		image["post"] = map[string]interface{}{
			"symlinks": extractLinuxSymlinks(symlink, entrypoint),
		}
	}
	return image
}

// ─── Chunk 4 · WINDOWS TARGET ───────────────────────────────────────────────

// windowsDeps builds the dependencies map for the windowscross target.
// Runtime deps are never allowed on windowscross — Dalec rejects them.
func windowsDeps() map[string]interface{} {
	repoInfo := pipeline.Current.RepoInfo

	buildDeps := map[string]interface{}{}
	if infraRepo.IsADORepo(repoInfo.GitURL) {
		buildDeps["git"] = map[string]interface{}{}
		if repoInfo.Generator == repository.GoModGenerator {
			buildDeps["msft-golang"] = goToolchainDep(pipeline.Current.RepoInfo.GoVersion)
		}
	}
	if repoInfo.Generator == repository.GoModGenerator {
		buildDeps["msft-golang"] = goToolchainDep(pipeline.Current.RepoInfo.GoVersion)
	}
	return map[string]interface{}{
		"build":       buildDeps,
		"extra_repos": []map[string]interface{}{msftWindowsExtraRepo()},
	}
}

// windowsArtifacts returns the per-target artifacts for windowscross (.exe binaries + license).
func windowsArtifacts() map[string]interface{} {
	return map[string]interface{}{
		"binaries": computeWindowsArtifactBinaries(),
		"licenses": map[string]interface{}{
			pipeline.Current.RepoInfo.Repo + "/" + pipeline.Current.RepoInfo.LicenseFile: map[string]interface{}{},
		},
	}
}

// windowsImageConfig builds the image map (entrypoint + symlink + optional bases) for the windowscross target.
// Base images are extracted from the parsed Dockerfile stages (not from the LLM).
func windowsImageConfig(binaryName string) map[string]interface{} {
	// binaryName is already resolved to the final Windows artifact (e.g. dropgz)
	// by resolveTestPaths via computeArtifactPaths.
	entrypoint := "/Windows/System32/" + binaryName + ".exe"

	image := map[string]interface{}{"entrypoint": entrypoint}

	if baseImages := findWindowsBaseImages(); len(baseImages) > 0 {
		var bases []map[string]interface{}
		for _, ref := range baseImages {
			bases = append(bases, map[string]interface{}{
				"rootfs": map[string]interface{}{
					"image": map[string]interface{}{
						"ref": ref,
					},
				},
			})
		}
		image["bases"] = bases
	}

	image["post"] = map[string]interface{}{
		"symlinks": extractWindowsSymlinks(entrypoint, binaryName),
	}
	return image
}

// findWindowsBaseImages scans parsed Dockerfile stages for Windows base images
// (nanoserver, servercore, etc.) and returns their full image refs.
func findWindowsBaseImages() []string {
	var refs []string
	for _, stage := range pipeline.Current.Dockerfile.Stages {
		from := strings.ToLower(stage.From)
		if strings.Contains(from, "nanoserver") ||
			strings.Contains(from, "servercore") ||
			strings.Contains(from, "windows") {
			refs = append(refs, stage.From)
		}
	}
	return refs
}

// ─── Chunk 5 · EXTRA REPOS ─────────────────────────────────────────────────

// msftLinuxExtraRepo returns the Microsoft apt extra_repos entry for a Debian/Ubuntu distro.
func msftLinuxExtraRepo(distro string) map[string]interface{} {
	return map[string]interface{}{
		"keys": map[string]interface{}{
			"microsoft.asc": map[string]interface{}{
				"http": map[string]interface{}{
					"url": "https://packages.microsoft.com/keys/microsoft.asc",
				},
			},
		},
		"config": map[string]interface{}{
			"microsoft-prod": map[string]interface{}{
				"inline": map[string]interface{}{
					"file": map[string]interface{}{
						"contents": fmt.Sprintf("deb [trusted=yes] %s %s main\n", microsoftRepoURI(distro), distro),
					},
				},
			},
		},
		"envs": []string{"build", "install", "test"},
	}
}

// microsoftRepoURI returns the packages.microsoft.com apt repo base URL for a distro.
func microsoftRepoURI(distro string) string {
	switch distro {
	case "bookworm":
		return "https://packages.microsoft.com/debian/12/prod"
	case "bullseye":
		return "https://packages.microsoft.com/debian/11/prod"
	case "noble":
		return "https://packages.microsoft.com/ubuntu/24.04/prod"
	case "jammy":
		return "https://packages.microsoft.com/ubuntu/22.04/prod"
	case "focal":
		return "https://packages.microsoft.com/ubuntu/20.04/prod"
	case "bionic":
		return "https://packages.microsoft.com/ubuntu/18.04/prod"
	default:
		return "https://packages.microsoft.com/" + distro + "/apt/prod"
	}
}

// msftWindowsExtraRepo returns the Microsoft Jammy apt extra_repos entry for the windowscross builder.
func msftWindowsExtraRepo() map[string]interface{} {
	return map[string]interface{}{
		"keys": map[string]interface{}{
			"microsoft.asc": map[string]interface{}{
				"http": map[string]interface{}{
					"url": "https://packages.microsoft.com/keys/microsoft.asc",
				},
			},
		},
		"config": map[string]interface{}{
			"microsoft-prod": map[string]interface{}{
				"inline": map[string]interface{}{
					"file": map[string]interface{}{
						"contents": "deb [trusted=yes] https://packages.microsoft.com/ubuntu/22.04/prod jammy main\n",
					},
				},
			},
		},
		"envs": []string{"build", "install", "test"},
	}
}

// ─── Chunk 6 · UTILITIES ────────────────────────────────────────────────────

// findPrimaryLinuxTarget returns the first non-windowscross SpecTarget.
func findPrimaryLinuxTarget(targets []contents.SpecTarget) *contents.SpecTarget {
	for i, ts := range targets {
		if ts.OS != "windowscross" {
			return &targets[i]
		}
	}
	return nil
}

// entrypointBinaryName derives the canonical binary name from the primary
// linux target's symlink path (the Dalec symlinks map key = real installed binary).
// Returns "" when no linux target or symlink is set.
func entrypointBinaryName(spec *contents.DockerfileSpec) string {
	if spec == nil {
		return ""
	}
	if lt := findPrimaryLinuxTarget(spec.Targets); lt != nil {
		return canonicalBase(lt.Symlink)
	}
	return ""
}

// canonicalBase extracts the file name component from a container path.
// e.g. "/dropgz" → "dropgz", "/usr/local/bin/azure-ipam" → "azure-ipam".
func canonicalBase(entrypoint string) string {
	if entrypoint == "" {
		return ""
	}
	if i := strings.LastIndex(entrypoint, "/"); i >= 0 {
		return entrypoint[i+1:]
	}
	return entrypoint
}

// goToolchainDep returns the msft-golang dependency entry.
// When goVersion is set (e.g. "1.24"), a version constraint is added.
// Otherwise, an empty constraint (any version) is returned.
func goToolchainDep(goVersion string) map[string]interface{} {
	if goVersion != "" {
		return map[string]interface{}{
			"version": []string{">=" + goVersion},
		}
	}
	return map[string]interface{}{}
}
