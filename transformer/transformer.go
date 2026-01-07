package transformer

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"dalec-mapping/github"
	"dalec-mapping/parser"
)

// DalecSpec represents a Dalec specification using flexible maps for dynamic keys
type DalecSpec map[string]interface{}

type BuildTarget string

const (
	AzLinux3Rpm           BuildTarget = "azlinux3/rpm"
	AzLinux3Container     BuildTarget = "azlinux3/container"
	NobleDeb              BuildTarget = "noble/deb"
	JammyDeb              BuildTarget = "jammy/deb"
	FocalDeb              BuildTarget = "focal/deb"
	BionicDeb             BuildTarget = "bionic/deb"
	BookwormDeb           BuildTarget = "bookworm/deb"
	WindowsCrossContainer BuildTarget = "windowscross/container"
)

// TODO: Per-target platforms

// DefaultSpec combines GitHub repo info and parsed Dockerfile info
type DefaultSpec struct {
	github.RepoInfo
	parser.DockerfileInfo

	BuildTargets []BuildTarget
}

func InitDefaultSpec(repoInfo *github.RepoInfo, dockerfileInfo *parser.DockerfileInfo) *DefaultSpec {
	defaultSpec := &DefaultSpec{}
	defaultSpec.RepoInfo = *repoInfo

	if dockerfileInfo != nil {
		defaultSpec.DockerfileInfo = *dockerfileInfo
	}

	defaultSpec.BuildTargets = []BuildTarget{
		AzLinux3Rpm,
		AzLinux3Container,
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
	args["Revision"] = defaultSpec.Revision
	args["Version"] = defaultSpec.Version
	args["Commit"] = defaultSpec.LatestCommit
	args["TARGETARCH"] = getArgValueOrDefault(defaultSpec, "TARGETARCH", "")
	args["TARGETOS"] = getArgValueOrDefault(defaultSpec, "TARGETOS", "")

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

	// TODO: Set default build target(s)
	ext["build-targets"] = defaultSpec.BuildTargets

	// Per-target configurations
	perTarget := make(map[string]interface{})
	perTarget["windowscross"] = map[string]interface{}{
		"platforms": []string{"windows/amd64"},
	}
	ext["per-target"] = perTarget

	return ext
}

// extractSources creates source definitions from Dockerfile
func extractSources(defaultSpec *DefaultSpec) map[string]interface{} {
	sources := make(map[string]interface{})
	sourceName := defaultSpec.Repo

	// TODO: Verify if sources need to be defined from actual builds in stages

	// Find builder stages with actual builds
	for _, stage := range defaultSpec.Stages {
		if !isBuilderStage(stage) {
			continue
		}

		source := make(map[string]interface{})

		git := make(map[string]interface{})
		git["url"] = defaultSpec.GitURL
		git["commit"] = "${COMMIT}"

		source["git"] = git

		// TODO: Check for language-specific generators
		source["generate"] = []map[string]interface{}{
			{string(defaultSpec.Generator): map[string]interface{}{}},
		}

		sources[sourceName] = source
		break // Use first builder stage
	}

	// Fallback if no builder found
	if len(sources) == 0 {
		source := make(map[string]interface{})
		git := make(map[string]interface{})
		git["url"] = defaultSpec.GitURL
		git["commit"] = "${COMMIT}"
		source["git"] = git
		source["generate"] = []map[string]interface{}{
			{"gomod": map[string]interface{}{}},
		}

		sources[sourceName] = source
	}

	return sources
}

// extractDependencies extracts build and runtime dependencies
func extractDependencies(defaultSpec *DefaultSpec) map[string]interface{} {
	deps := make(map[string]interface{})
	buildDeps := make(map[string]interface{})

	// Detect language/framework dependencies
	for _, stage := range defaultSpec.Stages {
		// Check for Go
		if hasGoModules(stage) || stage.From == "go" || strings.Contains(stage.From, "golang") {
			buildDeps["msft-golang"] = map[string]interface{}{}
		}

		// Check for package manager installs
		for _, run := range stage.Runs {
			run = strings.ToLower(run)
			// tdnf, yum, apt, etc.
			if strings.Contains(run, "tdnf install") || strings.Contains(run, "yum install") {
				// Could parse package names, for now leave as TODO
			}
		}
	}

	if len(buildDeps) > 0 {
		deps["build"] = buildDeps
	} else {
		deps["build"] = map[string]interface{}{
			"msft-golang": map[string]interface{}{},
		}
	}

	return deps
}

// extractTargets creates target-specific configurations
func extractTargets(defaultSpec *DefaultSpec) map[string]interface{} {
	targets := make(map[string]interface{})

	// Add standard Azure Linux target with required dependencies
	for _, buildTarget := range defaultSpec.BuildTargets {
		parts := strings.Split(string(buildTarget), "/")

		if len(parts) != 2 {
			fmt.Printf("❌ Invalid build target format: %s\n", buildTarget)
			fmt.Printf("    Expected format: os/platform (e.g., azlinux3/rpm)\n")
			os.Exit(1)
		}

		os, _ := parts[0], parts[1]

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
			// targets[string(buildTarget)] = target
			// }

			runtimeDeps["openssl-libs"] = map[string]interface{}{}
			runtimeDeps["SymCrypt"] = map[string]interface{}{}
			runtimeDeps["SymCrypt-OpenSSL"] = map[string]interface{}{}

			target["dependencies"] = map[string]interface{}{
				"runtime": runtimeDeps,
			}
			targets[string(buildTarget)] = target
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
	if len(steps) > 0 {
		build["steps"] = steps
	}

	return build
}

// extractBuildCommands extracts build commands from builder stages
func extractBuildCommands(defaultSpec *DefaultSpec) []map[string]interface{} {
	var steps []map[string]interface{}

	for _, stage := range defaultSpec.Stages {
		if !isBuilderStage(stage) {
			continue
		}

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
	artifacts["licenses"] = license

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

func isBuilderStage(stage parser.Stage) bool {
	name := strings.ToLower(stage.Name)
	return name == "builder" || strings.Contains(name, "build")
}

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
		if !isBuilderStage(stage) {
			continue
		}

		if stage.Name != "" {
			return stage.Name
		}
	}
	return "builder"
}

func getArgValueOrDefault(defaultSpec *DefaultSpec, key string, defaultValue any) any {
	if defaultSpec == nil {
		return fmt.Sprintf("%v", defaultValue)
	}

	if val, exists := defaultSpec.Args[key]; exists && val != "" {
		return val
	}

	return defaultValue
}

// Path-based helper functions for nested map manipulation

// Set sets a nested value using dot notation path
// Example: Set(spec, "build.env.VERSION", "1.0")
func Set(spec DalecSpec, path string, value interface{}) {
	keys := strings.Split(path, ".")
	current := spec

	for i := 0; i < len(keys)-1; i++ {
		key := keys[i]
		if _, exists := current[key]; !exists {
			current[key] = make(map[string]interface{})
		}
		if m, ok := current[key].(map[string]interface{}); ok {
			current = m
		} else {
			// Can't traverse further, recreate as map
			current[key] = make(map[string]interface{})
			current = current[key].(map[string]interface{})
		}
	}

	current[keys[len(keys)-1]] = value
}

// Get retrieves a nested value using dot notation path
// Example: Get(spec, "build.env.VERSION")
func Get(spec DalecSpec, path string) (interface{}, error) {
	keys := strings.Split(path, ".")
	var current interface{} = spec

	for _, key := range keys {
		if m, ok := current.(map[string]interface{}); ok {
			if val, exists := m[key]; exists {
				current = val
			} else {
				return nil, fmt.Errorf("key not found: %s in path %s", key, path)
			}
		} else {
			return nil, fmt.Errorf("not a map at key: %s in path %s", key, path)
		}
	}

	return current, nil
}
