package transformer

import (
	"dalec-mapping/domain/contents"
	"dalec-mapping/domain/llm"
	"dalec-mapping/domain/repository"
	"dalec-mapping/infrastructure/test"

	"fmt"
	"os"
	"strings"
)

// testPaths holds the resolved binary paths used for per-target file tests.
type testPaths struct {
	binaryName  string
	binaryPath  string // real installed file (has permissions)
	symlinkPath string // symlink (existence-only test)
}

// extractTargets builds the `targets:` map for every build target in the spec.
// Each unique OS (azlinux3, windowscross, jammy, …) gets exactly one entry.
func extractTargets(defaultSpec *contents.DefaultSpec, makefileInfo *contents.MakefileInfo, nonDeterministicValues *llm.NonDeterministicValues) map[string]interface{} {
	tp := resolveTestPaths(defaultSpec, nonDeterministicValues)

	targets := make(map[string]interface{})
	seen := make(map[string]bool)

	for _, buildTarget := range defaultSpec.BuildTargets {
		osName, ok := parseTargetOS(string(buildTarget))
		if !ok {
			fmt.Printf("❌ Invalid build target format: %s\n    Expected: os/platform (e.g. azlinux3/container)\n", buildTarget)
			os.Exit(1)
		}
		if seen[osName] {
			continue
		}
		seen[osName] = true
		targets[osName] = buildTargetEntry(osName, tp, defaultSpec, nonDeterministicValues)
	}
	return targets
}

// buildTargetEntry assembles the full target map for one OS.
func buildTargetEntry(osName string, tp testPaths, defaultSpec *contents.DefaultSpec, nonDeterministicValues *llm.NonDeterministicValues) map[string]interface{} {
	target := make(map[string]interface{})

	if osName == "windowscross" {
		target["dependencies"] = windowsDeps(defaultSpec, nonDeterministicValues)
		target["artifacts"] = windowsArtifacts(defaultSpec, nonDeterministicValues)
		target["image"] = windowsImageConfig(tp.binaryName, nonDeterministicValues)
	} else {
		target["dependencies"] = linuxDeps(osName, defaultSpec, nonDeterministicValues)
		target["image"] = linuxImageConfig(osName, tp.binaryName, nonDeterministicValues)
	}

	target["tests"] = []interface{}{
		test.TestCheckFiles(osName, tp.binaryName, tp.binaryPath, tp.symlinkPath, 0755),
	}
	return target
}

// resolveTestPaths determines the binary name and install paths used in file tests.
// Dalec installs artifacts.binaries to /usr/bin/ (the real file, tested with permissions).
// image.post.symlinks creates /usr/local/bin/ as a symlink (tested for existence only).
func resolveTestPaths(defaultSpec *contents.DefaultSpec, nonDeterministicValues *llm.NonDeterministicValues) testPaths {
	tp := testPaths{
		binaryName:  defaultSpec.SpecImageName,
		binaryPath:  "/usr/bin/" + defaultSpec.SpecImageName,
		symlinkPath: "/usr/local/bin/" + defaultSpec.SpecImageName,
	}
	if nonDeterministicValues == nil {
		return tp
	}
	if len(nonDeterministicValues.Binaries) > 0 && nonDeterministicValues.Binaries[0].Name != "" {
		tp.binaryName = nonDeterministicValues.Binaries[0].Name
		tp.binaryPath = "/usr/bin/" + tp.binaryName
		tp.symlinkPath = "/usr/local/bin/" + tp.binaryName
	}
	if lt := findPrimaryLinuxTarget(nonDeterministicValues.Targets); lt != nil {
		if lt.Symlink != "" {
			tp.binaryPath = lt.Symlink
			tp.symlinkPath = lt.Entrypoint
		} else if lt.Entrypoint != "" {
			tp.binaryPath = lt.Entrypoint
			tp.symlinkPath = ""
		}
	}
	return tp
}

// parseTargetOS splits "os/platform" and returns the OS portion.
func parseTargetOS(buildTarget string) (string, bool) {
	parts := strings.SplitN(buildTarget, "/", 2)
	if len(parts) != 2 {
		return "", false
	}
	return parts[0], true
}

// findTargetSpecByOS returns the TargetSpec whose TargetOS prefix matches osName.
func findTargetSpecByOS(targets []llm.TargetSpec, osName string) *llm.TargetSpec {
	for i, ts := range targets {
		if strings.SplitN(ts.TargetOS, "/", 2)[0] == osName {
			return &targets[i]
		}
	}
	return nil
}

// findPrimaryLinuxTarget returns the first non-windowscross TargetSpec.
func findPrimaryLinuxTarget(targets []llm.TargetSpec) *llm.TargetSpec {
	for i, ts := range targets {
		if !strings.HasPrefix(ts.TargetOS, "windowscross") {
			return &targets[i]
		}
	}
	return nil
}

// ── Linux targets ────────────────────────────────────────────────────────────

// linuxDeps builds the dependencies map for a Linux target.
func linuxDeps(osName string, defaultSpec *contents.DefaultSpec, nonDeterministicValues *llm.NonDeterministicValues) map[string]interface{} {
	buildDeps := map[string]interface{}{}
	runtimeDeps := map[string]interface{}{}

	switch osName {
	case "azlinux3":
		if defaultSpec.Generator == repository.GoModGenerator {
			buildDeps["msft-golang"] = map[string]interface{}{}
		}
		for _, pkg := range []string{"SymCrypt", "SymCrypt-OpenSSL", "openssl-libs"} {
			buildDeps[pkg] = map[string]interface{}{}
			runtimeDeps[pkg] = map[string]interface{}{}
		}
		mergeTargetDeps(buildDeps, runtimeDeps, osName, nonDeterministicValues)

	case "bookworm", "bullseye", "noble", "jammy", "focal", "bionic":
		if defaultSpec.Generator == repository.GoModGenerator {
			buildDeps["msft-golang"] = map[string]interface{}{}
			buildDeps["gcc"] = map[string]interface{}{}
		}
		mergeTargetDeps(buildDeps, runtimeDeps, osName, nonDeterministicValues)
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

// mergeTargetDeps copies LLM-provided build/runtime deps for osName into the maps.
func mergeTargetDeps(buildDeps, runtimeDeps map[string]interface{}, osName string, nonDeterministicValues *llm.NonDeterministicValues) {
	if nonDeterministicValues == nil {
		return
	}
	ts := findTargetSpecByOS(nonDeterministicValues.Targets, osName)
	if ts == nil {
		return
	}
	for _, dep := range ts.Build {
		buildDeps[dep] = map[string]interface{}{}
	}
	for _, dep := range ts.Runtime {
		runtimeDeps[dep] = map[string]interface{}{}
	}
}

// linuxImageConfig builds the image map (entrypoint + symlinks) for a Linux target.
func linuxImageConfig(osName, binaryName string, nonDeterministicValues *llm.NonDeterministicValues) map[string]interface{} {
	entrypoint := "/usr/local/bin/" + binaryName
	symlink := "/usr/bin/" + binaryName

	if nonDeterministicValues != nil {
		if ts := findTargetSpecByOS(nonDeterministicValues.Targets, osName); ts != nil {
			if ts.Entrypoint != "" {
				entrypoint = ts.Entrypoint
			}
			if ts.Symlink != "" {
				symlink = ts.Symlink
			}
		}
	}

	image := map[string]interface{}{"entrypoint": entrypoint}
	if symlink != "" {
		image["post"] = map[string]interface{}{
			"symlinks": extractLinuxSymlinks(symlink, entrypoint),
		}
	}
	return image
}

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

// ── windowscross target ───────────────────────────────────────────────────────

// windowsDeps builds the dependencies map for the windowscross target.
// Runtime deps are never allowed on windowscross — Dalec rejects them.
func windowsDeps(defaultSpec *contents.DefaultSpec, nonDeterministicValues *llm.NonDeterministicValues) map[string]interface{} {
	buildDeps := map[string]interface{}{}
	if defaultSpec.Generator == repository.GoModGenerator {
		buildDeps["msft-golang"] = map[string]interface{}{}
	}
	if nonDeterministicValues != nil {
		if ts := findTargetSpecByOS(nonDeterministicValues.Targets, "windowscross"); ts != nil {
			for _, dep := range ts.Build {
				buildDeps[dep] = map[string]interface{}{}
			}
			// ts.Runtime intentionally ignored — not allowed on windowscross
		}
	}
	return map[string]interface{}{
		"build":       buildDeps,
		"extra_repos": []map[string]interface{}{msftWindowsExtraRepo()},
	}
}

// windowsArtifacts returns the per-target artifacts for windowscross (.exe binaries + license).
func windowsArtifacts(defaultSpec *contents.DefaultSpec, nonDeterministicValues *llm.NonDeterministicValues) map[string]interface{} {
	return map[string]interface{}{
		"binaries": computeWindowsArtifactBinaries(defaultSpec, nonDeterministicValues),
		"licenses": map[string]interface{}{
			defaultSpec.Repo + "/LICENSE": map[string]interface{}{},
		},
	}
}

// windowsImageConfig builds the image map (entrypoint + symlink) for the windowscross target.
func windowsImageConfig(binaryName string, nonDeterministicValues *llm.NonDeterministicValues) map[string]interface{} {
	entrypoint := "/Windows/System32/" + binaryName + ".exe"
	if nonDeterministicValues != nil {
		if ts := findTargetSpecByOS(nonDeterministicValues.Targets, "windowscross"); ts != nil && ts.Entrypoint != "" {
			if strings.ContainsAny(ts.Entrypoint, "/\\") {
				entrypoint = ts.Entrypoint
			} else {
				entrypoint = "/Windows/System32/" + ts.Entrypoint + ".exe"
			}
		}
	}
	return map[string]interface{}{
		"entrypoint": entrypoint,
		"post": map[string]interface{}{
			"symlinks": extractWindowsSymlinks(entrypoint, binaryName),
		},
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
