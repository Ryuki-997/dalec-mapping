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

	// Default Build Targets in x-build-extensions (can be overridden by Makefile or LLM output)
	defaultSpec.BuildTargets = []contents.BuildTarget{
		contents.AzLinux3Container, // Primary container image target
		contents.AzLinux3Rpm,       // RPM package target
		contents.NobleDeb,          // Ubuntu/Debian package target
		contents.WindowsCrossContainer, // Windows container image target
	}

	return defaultSpec
}

// TransformToDalec converts parsed Dockerfile info to Dalec spec format
func TransformToDalec(defaultSpec *contents.DefaultSpec, makefileInfo *contents.MakefileInfo, nonDeterministicValues *llm.NonDeterministicValues) DalecSpec {
	spec := make(DalecSpec)

	// Add syntax header (special comment format)
	spec["# syntax"] = "ghcr.io/azure/dalec/frontend:latest"

	// Initialize args section
	spec["args"] = populateArgs(defaultSpec, makefileInfo)
	populateMetadata(defaultSpec, spec)

	// Build extensions section
	spec["x-build-extensions"] = buildExtensions(defaultSpec)

	// Transform Dockerfile content to Dalec sections
	spec["sources"] = extractSources(defaultSpec)
	spec["dependencies"] = extractDependencies(defaultSpec, nonDeterministicValues)
	spec["targets"] = extractTargets(defaultSpec)
	buildSection := extractBuildSection(defaultSpec, makefileInfo, nonDeterministicValues)
	spec["build"] = buildSection
	spec["artifacts"] = extractArtifacts(defaultSpec, nonDeterministicValues)
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

	// Per-target configurations
	perTarget := make(map[string]interface{})
	perTarget["windowscross"] = map[string]interface{}{
		"platforms": []string{"windows/amd64"},
	}
	ext["per-target"] = perTarget

	return ext
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

		switch os {
		case "azlinux3":
			fallthrough
		case "noble":
			fallthrough
		case "jammy":
			fallthrough
		case "focal":
			fallthrough
		case "bionic":
			fallthrough
		case "bookworm":
			fallthrough
		case "windowscross":
			fallthrough
		default:
			target := make(map[string]interface{})
			runtimeDeps := make(map[string]interface{})

			runtimeDeps["openssl-libs"] = map[string]interface{}{}
			runtimeDeps["SymCrypt"] = map[string]interface{}{}
			runtimeDeps["SymCrypt-OpenSSL"] = map[string]interface{}{}

			target["dependencies"] = map[string]interface{}{
				"runtime": runtimeDeps,
			}
			targets[os] = target
		}
	}
	return targets
}

// extractArtifacts identifies build artifacts (uses nonDeterministicValues if available)
func extractArtifacts(defaultSpec *contents.DefaultSpec, nonDeterministicValues *llm.NonDeterministicValues) map[string]interface{} {
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
			
			artifact := defaultSpec.Repo + "/" + outputPath
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
