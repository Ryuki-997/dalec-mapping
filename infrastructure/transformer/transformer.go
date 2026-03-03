package transformer

import (
	"dalec-mapping/domain/contents"
	"dalec-mapping/domain/llm"
	"dalec-mapping/domain/onboarding"
	"dalec-mapping/domain/repository"
	"dalec-mapping/infrastructure/github"
	"dalec-mapping/infrastructure/test"

	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// DalecSpec represents a Dalec specification using flexible maps for dynamic keys
type DalecSpec map[string]interface{}

func InitDefaultSpec(onboardInfo *onboarding.OnboardingInfo, repoInfo *repository.RepoInfo, dockerfileInfo *contents.DockerfileInfo, previousDalecSpecInfo PreviousDalecSpec) *contents.DefaultSpec {
	defaultSpec := &contents.DefaultSpec{}
	defaultSpec.RepoInfo = *repoInfo

	defaultSpec.OnboardingInfo = *onboardInfo

	if dockerfileInfo != nil {
		defaultSpec.DockerfileInfo = *dockerfileInfo
	}

	if repoInfo.Version != previousDalecSpecInfo.Args.Version {
		defaultSpec.Revision = 1
	} else {
		defaultSpec.Revision = previousDalecSpecInfo.Args.Revision + 1
	}

	// Default Build Targets (can be overridden by LLM-extracted targets)
	defaultSpec.BuildTargets = []contents.BuildTarget{
		contents.AzLinux3Container,
		contents.WindowsCrossContainer,
	}

	return defaultSpec
}

// TransformToDalec converts parsed Dockerfile info to Dalec spec format
func TransformToDalec(defaultSpec *contents.DefaultSpec, makefileInfo *contents.MakefileInfo, nonDeterministicValues *llm.NonDeterministicValues) DalecSpec {
	spec := make(DalecSpec)

	// Add syntax header (special comment format)
	spec["# syntax"] = "ghcr.io/azure/dalec/frontend:latest"

	// Compute build section first to discover which variables are referenced
	buildSection, referencedVars := extractBuildSection(defaultSpec, makefileInfo, nonDeterministicValues)
	spec["build"] = buildSection

	// Initialize args section — only include Makefile vars that are actually used
	spec["args"] = populateArgs(defaultSpec, makefileInfo, referencedVars)
	populateMetadata(defaultSpec, spec)

	// Build extensions section
	spec["x-build-extensions"] = buildExtensions(defaultSpec)

	// Transform Dockerfile content to Dalec sections
	spec["sources"] = extractSources(defaultSpec)
	spec["dependencies"] = extractDependencies(nonDeterministicValues)
	spec["targets"] = extractTargets(defaultSpec)
	spec["artifacts"] = extractArtifacts(defaultSpec, makefileInfo, nonDeterministicValues)
	spec["image"] = extractImageConfig(defaultSpec, nonDeterministicValues)
	spec["tests"] = appendTests(defaultSpec, nonDeterministicValues)

	return spec
}

// buildExtensions creates the x-build-extensions section
func buildExtensions(defaultSpec *contents.DefaultSpec) map[string]interface{} {
	ext := make(map[string]interface{})
	ext["image-name"] = defaultSpec.SpecImageName
	ext["repository"] = defaultSpec.SpecRepository

	// Set default build target(s)
	ext["build-targets"] = defaultSpec.BuildTargets

	// Per-target platform configuration (only if windowscross is in targets)
	for _, bt := range defaultSpec.BuildTargets {
		if string(bt) == "windowscross/container" {
			ext["per-target"] = map[string]interface{}{
				"windowscross": map[string]interface{}{
					"platforms": []string{"windows/amd64"},
				},
			}
			break
		}
	}

	return ext
}

// ResolveTargets validates and converts target strings from NonDeterministicValues
// into BuildTarget values. Returns nil if none provided (caller uses InitDefaultSpec defaults).
func ResolveTargets(targets []string) []contents.BuildTarget {
	if len(targets) == 0 {
		fmt.Println("ℹ️  No targets specified, using InitDefaultSpec defaults")
		return nil
	}

	resolved := []contents.BuildTarget{}
	for _, t := range targets {
		t = strings.TrimSpace(t)
		if bt, ok := contents.IsValidTarget(t); ok {
			resolved = append(resolved, bt)
		} else {
			fmt.Printf("⚠️  Ignoring unsupported target: %s\n", t)
		}
	}

	if len(resolved) == 0 {
		fmt.Println("⚠️  No valid targets found, keeping InitDefaultSpec defaults")
		return nil
	}

	return resolved
}

// microsoftRepoURI returns the packages.microsoft.com apt repo URI for a given distro.
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

// extractTargets creates target-specific configurations
func extractTargets(defaultSpec *contents.DefaultSpec) map[string]interface{} {
	targets := make(map[string]interface{})

	// Track unique OS targets (deduplicate by OS, not by full build target)
	osTargets := make(map[string]bool)

	// Add standard Azure Linux target with required dependencies
	for _, buildTarget := range defaultSpec.BuildTargets {
		parts := strings.Split(string(buildTarget), "/")

		if len(parts) != 2 {
			fmt.Printf("❌ Invalid build target format: %s\n", buildTarget)
			fmt.Printf("    Expected format: os/platform (e.g., azlinux3/rpm)\n")
			os.Exit(1)
		}

		os, _ := parts[0], parts[1]

		// Only add each OS target once (not per package format)
		if osTargets[os] {
			continue
		}
		osTargets[os] = true

		target := make(map[string]interface{})
		deps := make(map[string]interface{})
		buildDeps := make(map[string]interface{})

		// Per-target build dependencies and repo configuration
		switch os {
		case "azlinux3":
			// msft-golang is available natively in Azure Linux repos
			if defaultSpec.Generator == repository.GoModGenerator {
				buildDeps["msft-golang"] = map[string]interface{}{}
			}
			// SymCrypt/OpenSSL runtime deps only apply to Azure Linux (RPM packages)
			deps["runtime"] = map[string]interface{}{
				"openssl-libs":    map[string]interface{}{},
				"SymCrypt":        map[string]interface{}{},
				"SymCrypt-OpenSSL": map[string]interface{}{},
			}
		case "bookworm", "bullseye", "noble", "jammy", "focal", "bionic":
			// Debian/Ubuntu: add Microsoft apt repo to get msft-golang
			if defaultSpec.Generator == repository.GoModGenerator {
				buildDeps["msft-golang"] = map[string]interface{}{}
				buildDeps["gcc"] = map[string]interface{}{}
			}

			// Microsoft apt feed via extra_repos using proper Dalec Source format.
			// Dalec appends ".list" to the config key, so we use the traditional "deb ..." format.
			repoURI := microsoftRepoURI(os)
			deps["extra_repos"] = []map[string]interface{}{
				{
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
									"contents": fmt.Sprintf("deb [trusted=yes] %s %s main\n", repoURI, os),
								},
							},
						},
					},
					"envs": []string{"build", "install", "test"},
				},
			}
		case "windowscross":
			// windowscross builds on an Ubuntu (Jammy) base — needs extra_repos for msft-golang.
			// No SymCrypt/OpenSSL runtime deps (those are Linux RPM packages, not Windows).
			if defaultSpec.Generator == repository.GoModGenerator {
				buildDeps["msft-golang"] = map[string]interface{}{}
			}

			// Microsoft Jammy apt feed for the windowscross builder environment
			deps["extra_repos"] = []map[string]interface{}{
				{
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
				},
			}
		}

		if len(buildDeps) > 0 {
			deps["build"] = buildDeps
		}
		target["dependencies"] = deps

		// windowscross uses Windows paths — override the global Linux image config
		// Must suppress symlinks and set a Windows-compatible entrypoint
		if os == "windowscross" {
			target["image"] = map[string]interface{}{
				"entrypoint": defaultSpec.SpecImageName,
				"post":       map[string]interface{}{},
			}
			// Override global tests — Linux file permission checks don't apply to Windows
			target["tests"] = []interface{}{}
		}

		targets[os] = target
	}
	return targets
}

// extractArtifacts identifies build artifacts (uses nonDeterministicValues if available).
// When a binary's build command contains a leading "cd X &&", the subdir X is resolved
// via makefileInfo and prepended to the artifact path so it is root-relative.
func extractArtifacts(defaultSpec *contents.DefaultSpec, makefileInfo *contents.MakefileInfo, nonDeterministicValues *llm.NonDeterministicValues) map[string]interface{} {
	artifacts := make(map[string]interface{})
	binaries := make(map[string]interface{})

	// Use agent-extracted binary name if available
	if nonDeterministicValues != nil {
		for _, aux := range nonDeterministicValues.Binaries {
			outputPath := aux.OutputPath
			github.ClearEnvVariables("OutputPath", &outputPath)

			if outputPath == "" {
				outputPath = aux.Name
			}
			if !strings.Contains(outputPath, "/") {
				outputPath = fmt.Sprintf("bin/%s", outputPath)
			}

			// Detect "cd X &&" in the build command and fold X into the artifact path
			subdir, _ := extractCdDir(strings.TrimSpace(aux.BuildCommand))
			var artifact string
			if subdir != "" && makefileInfo != nil {
				resolvedSubdir := NestedValueReplacement(defaultSpec, makefileInfo, subdir)
				artifact = defaultSpec.Repo + "/" + resolvedSubdir + "/" + outputPath
			} else {
				artifact = defaultSpec.Repo + "/" + outputPath
			}
			binaries[artifact] = map[string]interface{}{}
			fmt.Printf("ARTIFACTS: %v\n", artifact)
		}
	} else {
		// Fallback to default
		binaries["bin/"+defaultSpec.Repo] = map[string]interface{}{}
	}

	artifacts["binaries"] = binaries

	return artifacts
}

// extractImageConfig extracts final image configuration (uses nonDeterministicValues if available)
func extractImageConfig(defaultSpec *contents.DefaultSpec, nonDeterministicValues *llm.NonDeterministicValues) map[string]interface{} {
	image := make(map[string]interface{})

	var entrypoint string
	var symlink string

	// Use agent-extracted values if available
	if nonDeterministicValues != nil && nonDeterministicValues.Entrypoint != "" {
		entrypoint = nonDeterministicValues.Entrypoint
		symlink = nonDeterministicValues.Symlink
	} else {
		// Fallback to repo name
		entrypoint = "/" + defaultSpec.Repo
		symlink = "/usr/bin/" + defaultSpec.Repo
	}

	image["entrypoint"] = entrypoint

	// Create symlinks
	symlinks := make(map[string]interface{})
	symlinks[symlink] = map[string]interface{}{
		"path": entrypoint,
	}

	image["post"] = map[string]interface{}{
		"symlinks": symlinks,
	}

	return image
}

// createSymlinks creates symlink configuration for binaries
func createSymlinks(stage *contents.Stage) map[string]interface{} {
	post := make(map[string]interface{})
	symlinks := make(map[string]interface{})

	for _, copy := range stage.Copies {
		if copy.From != "builder" && !strings.Contains(copy.From, "build") {
			continue
		}

		for _, src := range copy.Source {
			if !strings.Contains(src, "/bin/") || !strings.Contains(copy.Dest, "/usr/local/bin/") {
				continue
			}

			// Create symlink from standard location to actual location
			binaryName := filepath.Base(src)
			destPath := filepath.Join(copy.Dest, binaryName)
			if !strings.HasSuffix(copy.Dest, binaryName) {
				destPath = copy.Dest
			}

			symlinks["/usr/bin/"+binaryName] = map[string]interface{}{
				"path": destPath,
			}
		}
	}

	if len(symlinks) > 0 {
		post["symlinks"] = symlinks
	}

	return post
}

// appendTests creates test specifications
func appendTests(defaultSpec *contents.DefaultSpec, nonDeterministicValues *llm.NonDeterministicValues) []map[string]interface{} {
	tests := make([]map[string]interface{}, 0)

	// Use binary name from first binary in list if available, otherwise fall back to repo name
	binaryName := defaultSpec.Repo
	if nonDeterministicValues != nil && len(nonDeterministicValues.Binaries) > 0 && nonDeterministicValues.Binaries[0].Name != "" {
		binaryName = nonDeterministicValues.Binaries[0].Name
	}

	tests = append(tests, test.TestCheckFiles(binaryName, 0755))

	return tests
}
