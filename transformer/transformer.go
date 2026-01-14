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

// DefaultSpec combines GitHub repo info and parsed Dockerfile info
type DefaultSpec struct {
	github.RepoInfo
	parser.DockerfileInfo

	Revision     int
	BuildTargets []cli.BuildTarget
}

func InitDefaultSpec(repoInfo *github.RepoInfo, dockerfileInfo *parser.DockerfileInfo, previousDalecSpecInfo PreviousDalecSpec) *DefaultSpec {
	defaultSpec := &DefaultSpec{}
	defaultSpec.RepoInfo = *repoInfo

	fmt.Printf("------------- Default Spec Generator: ------------- %s\n", defaultSpec.Generator)

	if dockerfileInfo != nil {
		defaultSpec.DockerfileInfo = *dockerfileInfo
	}

	fmt.Printf("Repo Version: %s, Previous Version: %s\n", repoInfo.Version, previousDalecSpecInfo.Args.Version)

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
func TransformToDalec(defaultSpec *DefaultSpec) DalecSpec {
	spec := make(DalecSpec)

	// Add syntax header (special comment format)
	spec["# syntax"] = "ghcr.io/azure/dalec/frontend:latest"

	// Initialize args section
	spec["args"] = populateArgs(defaultSpec)
	populateMetadata(defaultSpec, spec)

	// Build extensions section
	spec["x-build-extensions"] = buildExtensions(defaultSpec)

	// Transform Dockerfile content to Dalec sections
	spec["sources"] = extractSources(defaultSpec)
	spec["dependencies"] = extractDependencies(defaultSpec)
	spec["targets"] = extractTargets(defaultSpec)
	spec["build"] = extractBuildSteps(defaultSpec)
	spec["artifacts"] = extractArtifacts(defaultSpec)
	spec["image"] = extractImageConfig(defaultSpec)
	spec["tests"] = appendTests(defaultSpec)

	return spec
}

func populateArgs(defaultSpec *DefaultSpec) map[string]interface{} {
	args := make(map[string]interface{})
	args["REVISION"] = defaultSpec.Revision
	args["VERSION"] = defaultSpec.Version
	args["COMMIT"] = defaultSpec.LatestCommit

	targetArgs := map[string]bool{
		"ARCH":       true,
		"OS":         true,
		"OS_VERSION": true,
		"VERSION":    true,
	}

	for k, v := range defaultSpec.Args {
		if targetArgs[k] {
			continue
		}
		args[k] = v
	}

	return args
}

func populateMetadata(defaultSpec *DefaultSpec, spec DalecSpec) {

	spec["name"] = strings.ToLower(defaultSpec.Repo)
	spec["packager"] = "Azure Container Upstream"
	spec["vendor"] = "Microsoft Corporation"
	spec["license"] = defaultSpec.License
	spec["website"] = defaultSpec.GitURL
	spec["description"] = defaultSpec.Description
	spec["version"] = "${VERSION}"
	spec["revision"] = "${REVISION}"
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

// extractBuildSteps converts RUN commands to Dalec build steps
func extractBuildSteps(defaultSpec *DefaultSpec) map[string]interface{} {
	build := make(map[string]interface{})

	// Extract environment variables
	env := make(map[string]string)
	env["VERSION"] = "${VERSION}"

	// Collect env vars from builder stages
	// for _, stage := range defaultSpec.Stages {
	// 	if !isBuilderStage(stage) {
	// 		continue
	// 	}

	// 	for k, v := range stage.Env {
	// 		// Skip build args that are already in args section
	// 		if k != "OS" && k != "ARCH" && k != "VERSION" {
	// 			env[k] = v
	// 		}
	// 	}

	// 	// Add Go-specific env vars if it's a Go build
	// 	if !hasGoModules(stage) {
	// 		continue
	// 	}

	// env["GOPROXY"] = "direct"
	// env["GOEXPERIMENT"] = "systemcrypto"
	// env["CGO_ENABLED"] = "1"
	// }

	// if len(env) > 0 {
	// 	build["env"] = env
	// }

	env["GOPROXY"] = "direct"
	env["GOEXPERIMENT"] = "systemcrypto"
	env["CGO_ENABLED"] = "1"
	build["env"] = env

	// Extract build steps
	steps := extractBuildCommands(defaultSpec)
	if len(steps) == 0 {
		buildCommand := fmt.Sprintf("cd %s\ngo build -o bin/%s ./main.go", defaultSpec.Repo, defaultSpec.Repo)
		steps = []map[string]interface{}{
			{"command": buildCommand},
		}
	}

	build["steps"] = steps

	return build
}

// extractBuildCommands extracts build commands from builder stages
func extractBuildCommands(defaultSpec *DefaultSpec) []map[string]interface{} {
	var steps []map[string]interface{}

	for _, stage := range defaultSpec.Stages {

		if len(stage.Runs) == 0 {
			continue
		}

		// Combine relevant build commands
		var commands []string
		for _, run := range stage.Runs {
			// Filter out package installations (they go in dependencies)
			if !strings.Contains(run, "apt-get") &&
				!strings.Contains(run, "yum install") &&
				!strings.Contains(run, "tdnf install") {
				commands = append(commands, run)
			}
		}

		if len(commands) == 0 {
			continue
		}

		// Add workdir context if needed
		cmd := strings.Join(commands, "\n")
		if stage.Workdir != "" && !strings.Contains(cmd, "cd ") {
			cmd = "cd " + stage.Workdir + "\n" + cmd
		}

		steps = append(steps, map[string]interface{}{
			"command": cmd,
		})
	}

	return steps
}

// extractArtifacts identifies build artifacts
func extractArtifacts(defaultSpec *DefaultSpec) map[string]interface{} {
	artifacts := make(map[string]interface{})
	binaries := make(map[string]interface{})
	license := make(map[string]interface{})

	// Find binaries from COPY --from=builder in final stages
	// builderName := findBuilderStageName(defaultSpec)

	// for i := len(defaultSpec.Stages) - 1; i >= 0; i-- {
	// 	stage := defaultSpec.Stages[i]
	// 	// Skip builder stages, look at final stages
	// 	if isBuilderStage(stage) {
	// 		continue
	// 	}

	// 	for _, copy := range stage.Copies {
	// 		if copy.From != builderName && copy.From != "builder" {
	// 			continue
	// 		}
	// 		for _, src := range copy.Source {
	// 			// Check if it's a binary path
	// 			if strings.Contains(src, "/bin/") || strings.HasSuffix(src, ".exe") {
	// 				binaries[src] = map[string]interface{}{}
	// 			}
	// 		}
	// 	}
	// }

	// if len(binaries) > 0 {
	// 	artifacts["binaries"] = binaries
	// }

	binaryPath := defaultSpec.Repo + "/bin/" + defaultSpec.Repo
	binaries[binaryPath] = map[string]interface{}{}

	licensePath := defaultSpec.Repo + "/LICENSE"
	license[licensePath] = map[string]interface{}{}

	artifacts["binaries"] = binaries

	// TODO: Add license artifact if necessary
	// artifacts["licenses"] = license

	// Add licenses placeholder

	return artifacts
}

// extractImageConfig extracts final image configuration
func extractImageConfig(defaultSpec *DefaultSpec) map[string]interface{} {
	image := make(map[string]interface{})

	// if len(defaultSpec.Stages) == 0 {
	// 	return image
	// }

	// // Find the final Linux stage (skip Windows)
	// var finalStage *parser.Stage
	// for i := len(defaultSpec.Stages) - 1; i >= 0; i-- {
	// 	stage := &defaultSpec.Stages[i]
	// 	if stage.Name == "windows" || stage.Name == "hpc" {
	// 		continue
	// 	}

	// 	if len(stage.Entrypoint) > 0 || len(stage.Copies) > 0 {
	// 		finalStage = stage
	// 		break
	// 	}
	// }

	// if finalStage == nil {
	// 	return image
	// }

	// // Extract entrypoint
	// if len(finalStage.Entrypoint) > 0 {
	// 	entrypoint := finalStage.Entrypoint[0]
	// 	// If shell-wrapped, extract actual command
	// 	if len(finalStage.Entrypoint) > 2 && finalStage.Entrypoint[0] == "/bin/sh" {
	// 		entrypoint = finalStage.Entrypoint[2]
	// 	}
	// 	image["entrypoint"] = entrypoint
	// }

	// // Create symlinks for binaries if needed
	// post := createSymlinks(finalStage)
	// if len(post) > 0 {
	// 	image["post"] = post
	// }

	entrypoint := fmt.Sprintf("/%s", defaultSpec.Repo)
	post := make(map[string]interface{})
	symlinks := make(map[string]interface{})
	symlinks["/usr/bin/"+defaultSpec.Repo] = map[string]interface{}{
		"path": entrypoint,
	}
	post["symlinks"] = symlinks

	image["entrypoint"] = entrypoint
	image["post"] = post
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

func appendTests(defaultSpec *DefaultSpec) []map[string]interface{} {
	tests := []map[string]interface{}{
		TestCheckFiles(defaultSpec.Repo, 0755),
	}

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
