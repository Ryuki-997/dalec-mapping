package transformer

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"dalec/cli"
	"dalec/github"
	"dalec/parser"
)

// DalecSpec represents a Dalec specification using flexible maps for dynamic keys
type DalecSpec map[string]interface{}

// DefaultSpec combines GitHub repo info, parsed Dockerfile info, and Makefile info
type DefaultSpec struct {
	github.RepoInfo
	parser.DockerfileInfo

	Revision     int
	BuildTargets []cli.BuildTarget
}

func InitDefaultSpec(repoInfo *github.RepoInfo, dockerfileInfo *parser.DockerfileInfo, previousDalecSpecInfo PreviousDalecSpec) *DefaultSpec {
	defaultSpec := &DefaultSpec{}
	defaultSpec.RepoInfo = *repoInfo

	if dockerfileInfo != nil {
		defaultSpec.DockerfileInfo = *dockerfileInfo
	}

	if repoInfo.Version != previousDalecSpecInfo.Args.Version {
		defaultSpec.Revision = 1
	} else {
		defaultSpec.Revision = previousDalecSpecInfo.Args.Revision + 1
	}

	defaultSpec.BuildTargets = []cli.BuildTarget{
		cli.AzLinux3Container, // Primary container image target
		cli.AzLinux3Rpm,       // RPM package target
		cli.NobleDeb,          // Ubuntu/Debian package target
	}

	return defaultSpec
}

// TransformToDalec converts parsed Dockerfile info to Dalec spec format
func TransformToDalec(defaultSpec *DefaultSpec, makefileInfo *parser.MakefileInfo, nonDeterministicValues *parser.NonDeterministicValues) DalecSpec {
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
	buildSection, binaryPath := extractBuildSection(defaultSpec, nonDeterministicValues)
	spec["build"] = buildSection
	spec["artifacts"] = extractArtifacts(defaultSpec, binaryPath, nonDeterministicValues)
	spec["image"] = extractImageConfig(defaultSpec, nonDeterministicValues)
	spec["tests"] = appendTests(defaultSpec)

	return spec
}

// buildExtensions creates the x-build-extensions section
func buildExtensions(defaultSpec *DefaultSpec) map[string]interface{} {
	ext := make(map[string]interface{})
	ext["image-name"] = strings.ToLower(defaultSpec.Repo)
	ext["repository"] = "azure"

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
func extractTargets(defaultSpec *DefaultSpec) map[string]interface{} {
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

			// // Check if this is a Go binary (requires crypto dependencies)
			// hasGo := false
			// for _, stage := range defaultSpec.Stages {
			// 	if !hasGoModules(stage) {
			// 		continue
			// 	}

			// 	hasGo = true
			// 	break
			// }

			// if hasGo {
			// runtimeDeps["openssl-libs"] = map[string]interface{}{}
			// runtimeDeps["SymCrypt"] = map[string]interface{}{}
			// runtimeDeps["SymCrypt-OpenSSL"] = map[string]interface{}{}
			// }

			// if len(runtimeDeps) > 0 {
			// target["dependencies"] = map[string]interface{}{
			// 	"runtime": runtimeDeps,
			// }
			// targets[os] = target
			// }

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
func extractArtifacts(defaultSpec *DefaultSpec, binaryPath string, nonDeterministicValues *parser.NonDeterministicValues) map[string]interface{} {
	artifacts := make(map[string]interface{})
	binaries := make(map[string]interface{})

	// Use agent-extracted binary name if available
	if nonDeterministicValues != nil && nonDeterministicValues.BinaryName != "" {
		binaries[defaultSpec.Repo+"/"+nonDeterministicValues.BinaryName] = map[string]interface{}{}

		// Add auxiliary binaries
		for _, aux := range nonDeterministicValues.AuxiliaryBinaries {
			binaries[defaultSpec.Repo+"/"+aux.Name] = map[string]interface{}{}
		}
	} else if binaryPath != "" {
		binaries[binaryPath] = map[string]interface{}{}
	} else {
		// Fallback to default
		binaries["bin/"+defaultSpec.Repo] = map[string]interface{}{}
	}

	artifacts["binaries"] = binaries

	return artifacts
}

// extractImageConfig extracts final image configuration (uses nonDeterministicValues if available)
func extractImageConfig(defaultSpec *DefaultSpec, nonDeterministicValues *parser.NonDeterministicValues) map[string]interface{} {
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
func createSymlinks(stage *parser.Stage) map[string]interface{} {
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
func appendTests(defaultSpec *DefaultSpec) []map[string]interface{} {
	tests := make([]map[string]interface{}, 0)

	tests = append(tests, TestCheckFiles(defaultSpec.Repo, 0755))

	return tests
}

// Helper functions

func hasGoModules(stage parser.Stage) bool {
	for _, run := range stage.Runs {
		if strings.Contains(run, "go build") || strings.Contains(run, "go mod") {
			return true
		}
	}
	return false
}

func deriveSourceName(stage parser.Stage) string {
	// Try to derive from workdir
	if stage.Workdir != "" {
		name := filepath.Base(stage.Workdir)
		if name != "" && name != "/" && name != "." {
			return name
		}
	}
	return "source"
}

func findBuilderStageName(defaultSpec *DefaultSpec) string {
	for _, stage := range defaultSpec.Stages {
		// TODO: Implement isBuilderStage check
		// if !isBuilderStage(stage) {
		// 	continue
		// }

		if stage.Name != "" && (stage.Name == "builder" || strings.Contains(strings.ToLower(stage.Name), "build")) {
			return stage.Name
		}
	}
	return "builder"
}

func PrintDockerfileInfo(defaultSpec *DefaultSpec) {
	fmt.Println("Parsed Dockerfile Stages:")
	fmt.Println("")

	for _, stage := range defaultSpec.Stages {
		fmt.Printf("Stage: %s (From: %s)\n", stage.Name, stage.From)
		fmt.Println("  Env:")
		for k, v := range stage.Env {
			fmt.Printf("    %s=%s\n", k, v)
		}
		fmt.Println("  Runs:")
		for _, run := range stage.Runs {
			fmt.Printf("    %s\n", run)
		}
		fmt.Println("  Copies:")
		for _, copy := range stage.Copies {
			fmt.Printf("    From: %s, Source: %v, Dest: %s\n", copy.From, copy.Source, copy.Dest)
		}
		fmt.Println("")
	}

	for k, v := range defaultSpec.Args {
		fmt.Printf("Build Arg: %s=%s\n", k, v)
	}
	fmt.Println("")
}
