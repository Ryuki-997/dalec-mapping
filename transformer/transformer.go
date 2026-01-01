package transformer

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"dalec-mapping/parser"
)

// DalecSpec represents a Dalec specification using flexible maps for dynamic keys
// Using map[string]interface{} allows us to handle:
// 1. Dynamic keys (e.g., source names, dependency names)
// 2. Nested structures without defining every possible field
// 3. Easy YAML serialization
type DalecSpec map[string]interface{}

// RepoMetadata contains repository information (can be nil)
type RepoMetadata struct {
	GitURL      string
	Commit      string
	Website     string
	Description string
	License     string
	RepoName    string
}

// TransformToDalec converts parsed Dockerfile info to Dalec spec format
// repoMeta can be nil if no repository metadata is available
func TransformToDalec(repoInfo *RepoMetadata, previousSpec PreviousDalecSpec, dockerInfo *parser.DockerfileInfo) DalecSpec {
	rebuild(repoInfo, previousSpec)

	spec := make(DalecSpec)

	// Add syntax header (special comment format)
	spec["# syntax"] = "ghcr.io/azure/dalec/frontend:latest"

	// Initialize args section
	spec["args"] = populateArgs(repoInfo, dockerInfo)

	packageName := derivePackageName(dockerInfo)
	if repoInfo != nil && repoInfo.RepoName != "" {
		packageName = strings.ToLower(repoInfo.RepoName)
	}
	spec["name"] = packageName
	populateMetadata(spec, repoInfo)

	// Build extensions section
	spec["x-build-extensions"] = buildExtensions(packageName)

	// Transform Dockerfile content to Dalec sections
	if dockerInfo != nil {
		spec["sources"] = extractSources(dockerInfo, repoInfo)
		spec["dependencies"] = extractDependencies(dockerInfo)
		spec["targets"] = extractTargets(dockerInfo)
		spec["build"] = extractBuildSteps(dockerInfo)
		spec["artifacts"] = extractArtifacts(dockerInfo)
		spec["image"] = extractImageConfig(dockerInfo)
	}
	spec["tests"] = []map[string]interface{}{} // Empty placeholder

	return spec
}

func rebuild(repoInfo *RepoMetadata, previousSpec PreviousDalecSpec) bool {
	if previousSpec.Commit == "" {
		return false
	}

	if previousSpec.Commit == repoInfo.Commit {
		prevRevision, err := strconv.Atoi(previousSpec.Revision)
		if err != nil {
			prevRevision = 0
			fmt.Printf("⚠️  Warning: invalid previous revision '%s', resetting to 1\n", previousSpec.Revision)
			return false
		}

		previousSpec.Revision = fmt.Sprintf("%d", prevRevision+1)
		return false
	}

	return true
}

func populateArgs(repoMeta *RepoMetadata, dockerInfo *parser.DockerfileInfo) map[string]interface{} {
	if dockerInfo == nil {
		return map[string]interface{}{
			"REVISION":   "1",
			"VERSION":    "0.1",
			"COMMIT":     "",
			"TARGETARCH": "",
			"TARGETOS":   "",
		}
	}

	args := make(map[string]interface{})
	args["REVISION"] = getArgValueOrDefault(dockerInfo, "REVISION", "1")
	args["VERSION"] = getArgValueOrDefault(dockerInfo, "VERSION", "0.1")

	// Use commit from repo metadata if available
	commitValue := ""
	if repoMeta != nil && repoMeta.Commit != "" {
		commitValue = repoMeta.Commit
	}
	args["COMMIT"] = getArgValueOrDefault(dockerInfo, "COMMIT", commitValue)
	args["TARGETARCH"] = getArgValueOrDefault(dockerInfo, "TARGETARCH", "")
	args["TARGETOS"] = getArgValueOrDefault(dockerInfo, "TARGETOS", "")

	return args
}

func populateMetadata(spec DalecSpec, repoMeta *RepoMetadata) {

	// Standard metadata fields - use repo metadata if available
	spec["packager"] = "Azure Container Upstream"
	spec["vendor"] = "Microsoft Corporation"

	if repoMeta != nil && repoMeta.License != "" {
		spec["license"] = repoMeta.License
	} else {
		spec["license"] = "" // TODO: needs manual input
	}

	if repoMeta != nil && repoMeta.Website != "" {
		spec["website"] = repoMeta.Website
	} else {
		spec["website"] = "" // TODO: needs manual input
	}

	if repoMeta != nil && repoMeta.Description != "" {
		spec["description"] = repoMeta.Description
	} else {
		spec["description"] = "" // TODO: needs manual input
	}

	spec["version"] = "${VERSION}"
	spec["revision"] = "${REVISION}"
}

// TODO: Understand docker stages for package naming
// derivePackageName extracts a package name from Dockerfile info
func derivePackageName(info *parser.DockerfileInfo) string {
	if info == nil || len(info.Stages) == 0 {
		return "package"
	}

	// Look at final stage names
	for i := len(info.Stages) - 1; i >= 0; i-- {
		stage := info.Stages[i]
		if stage.Name != "" && stage.Name != "builder" && stage.Name != "build" {
			// Use stage name if it's a meaningful final stage
			if stage.Name == "linux" || stage.Name == "windows" {
				continue // Skip OS-specific stages
			}
			return strings.ToLower(stage.Name)
		}
	}

	// Check for binary names in COPY instructions
	for i := len(info.Stages) - 1; i >= 0; i-- {
		for _, copy := range info.Stages[i].Copies {
			if copy.From == "builder" {
				for _, src := range copy.Source {
					if strings.Contains(src, "/bin/") {
						name := filepath.Base(src)
						name = strings.TrimSuffix(name, ".exe")
						if name != "" {
							return strings.ToLower(name)
						}
					}
				}
			}
		}
	}

	return "package"
}

// buildExtensions creates the x-build-extensions section
func buildExtensions(packageName string) map[string]interface{} {
	ext := make(map[string]interface{})
	ext["image-name"] = strings.ToLower(packageName)
	ext["repository"] = "azure"
	ext["build-targets"] = []string{
		"azlinux3/rpm",
		"azlinux3/container",
		"windowscross/container",
	}

	// Per-target configurations
	perTarget := make(map[string]interface{})
	perTarget["windowscross"] = map[string]interface{}{
		"platforms": []string{"windows/amd64"},
	}
	ext["per-target"] = perTarget

	return ext
}

// extractSources creates source definitions from Dockerfile
func extractSources(info *parser.DockerfileInfo, repoMeta *RepoMetadata) map[string]interface{} {
	sources := make(map[string]interface{})

	// Determine source name from repo metadata or derive from Dockerfile
	sourceName := "source"
	if repoMeta != nil && repoMeta.RepoName != "" {
		sourceName = repoMeta.RepoName
	}

	// Find builder stages with actual builds
	for _, stage := range info.Stages {
		if isBuilderStage(stage) {
			if sourceName == "source" {
				sourceName = deriveSourceName(stage)
			}

			source := make(map[string]interface{})

			// Git source - use repo metadata if available
			git := make(map[string]interface{})
			if repoMeta != nil && repoMeta.GitURL != "" {
				git["url"] = repoMeta.GitURL
			} else {
				git["url"] = "" // TODO: needs manual input
			}
			git["commit"] = "${COMMIT}"
			source["git"] = git

			// Check for language-specific generators
			if hasGoModules(stage) {
				source["generate"] = []map[string]interface{}{
					{"gomod": map[string]interface{}{}},
				}
			}

			sources[sourceName] = source
			break // Use first builder stage
		}
	}

	// Fallback if no builder found
	if len(sources) == 0 {
		source := make(map[string]interface{})
		git := make(map[string]interface{})

		if repoMeta != nil && repoMeta.GitURL != "" {
			git["url"] = repoMeta.GitURL
		} else {
			git["url"] = ""
		}
		git["commit"] = "${COMMIT}"
		source["git"] = git

		sources[sourceName] = source
	}

	return sources
}

// extractDependencies extracts build and runtime dependencies

// extractDependencies extracts build and runtime dependencies
func extractDependencies(info *parser.DockerfileInfo) map[string]interface{} {
	deps := make(map[string]interface{})
	buildDeps := make(map[string]interface{})

	// Detect language/framework dependencies
	for _, stage := range info.Stages {
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
	}

	return deps
}

// extractTargets creates target-specific configurations
func extractTargets(info *parser.DockerfileInfo) map[string]interface{} {
	targets := make(map[string]interface{})

	// Add standard Azure Linux target with required dependencies
	azlinux3 := make(map[string]interface{})
	runtimeDeps := make(map[string]interface{})

	// Check if this is a Go binary (requires crypto dependencies)
	hasGo := false
	for _, stage := range info.Stages {
		if hasGoModules(stage) {
			hasGo = true
			break
		}
	}

	if hasGo {
		runtimeDeps["openssl-libs"] = map[string]interface{}{}
		runtimeDeps["SymCrypt"] = map[string]interface{}{}
		runtimeDeps["SymCrypt-OpenSSL"] = map[string]interface{}{}
	}

	if len(runtimeDeps) > 0 {
		azlinux3["dependencies"] = map[string]interface{}{
			"runtime": runtimeDeps,
		}
		targets["azlinux3"] = azlinux3
	}

	return targets
}

// extractBuildSteps converts RUN commands to Dalec build steps
func extractBuildSteps(info *parser.DockerfileInfo) map[string]interface{} {
	build := make(map[string]interface{})

	// Extract environment variables
	env := make(map[string]string)
	env["VERSION"] = "${VERSION}"

	// Collect env vars from builder stages
	for _, stage := range info.Stages {
		if isBuilderStage(stage) {
			for k, v := range stage.Env {
				// Skip build args that are already in args section
				if k != "OS" && k != "ARCH" && k != "VERSION" {
					env[k] = v
				}
			}

			// Add Go-specific env vars if it's a Go build
			if hasGoModules(stage) {
				env["GOPROXY"] = "direct"
				env["GOEXPERIMENT"] = "systemcrypto"
				env["CGO_ENABLED"] = "1"
			}
		}
	}

	if len(env) > 0 {
		build["env"] = env
	}

	// Extract build steps
	steps := extractBuildCommands(info)
	if len(steps) > 0 {
		build["steps"] = steps
	}

	return build
}

// extractBuildCommands extracts build commands from builder stages
func extractBuildCommands(info *parser.DockerfileInfo) []map[string]interface{} {
	var steps []map[string]interface{}

	for _, stage := range info.Stages {
		if isBuilderStage(stage) {
			if len(stage.Runs) > 0 {
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

				if len(commands) > 0 {
					// Add workdir context if needed
					cmd := strings.Join(commands, "\n")
					if stage.Workdir != "" && !strings.Contains(cmd, "cd ") {
						cmd = "cd " + stage.Workdir + "\n" + cmd
					}

					steps = append(steps, map[string]interface{}{
						"command": cmd,
					})
				}
			}
		}
	}

	return steps
}

// extractArtifacts identifies build artifacts
func extractArtifacts(info *parser.DockerfileInfo) map[string]interface{} {
	artifacts := make(map[string]interface{})
	binaries := make(map[string]interface{})

	// Find binaries from COPY --from=builder in final stages
	builderName := findBuilderStageName(info)

	for i := len(info.Stages) - 1; i >= 0; i-- {
		stage := info.Stages[i]

		// Skip builder stages, look at final stages
		if isBuilderStage(stage) {
			continue
		}

		for _, copy := range stage.Copies {
			if copy.From == builderName || copy.From == "builder" {
				for _, src := range copy.Source {
					// Check if it's a binary path
					if strings.Contains(src, "/bin/") || strings.HasSuffix(src, ".exe") {
						binaries[src] = map[string]interface{}{}
					}
				}
			}
		}
	}

	if len(binaries) > 0 {
		artifacts["binaries"] = binaries
	}

	// Add licenses placeholder
	// artifacts["licenses"] = map[string]interface{}{
	// 	"# TODO: Add LICENSE file path": map[string]interface{}{},
	// }

	return artifacts
}

// extractImageConfig extracts final image configuration
func extractImageConfig(info *parser.DockerfileInfo) map[string]interface{} {
	image := make(map[string]interface{})

	if len(info.Stages) == 0 {
		return image
	}

	// Find the final Linux stage (skip Windows)
	var finalStage *parser.Stage
	for i := len(info.Stages) - 1; i >= 0; i-- {
		stage := &info.Stages[i]
		if stage.Name != "windows" && stage.Name != "hpc" {
			if len(stage.Entrypoint) > 0 || len(stage.Copies) > 0 {
				finalStage = stage
				break
			}
		}
	}

	if finalStage == nil {
		return image
	}

	// Extract entrypoint
	if len(finalStage.Entrypoint) > 0 {
		entrypoint := finalStage.Entrypoint[0]
		// If shell-wrapped, extract actual command
		if len(finalStage.Entrypoint) > 2 && finalStage.Entrypoint[0] == "/bin/sh" {
			entrypoint = finalStage.Entrypoint[2]
		}
		image["entrypoint"] = entrypoint
	}

	// Create symlinks for binaries if needed
	post := createSymlinks(finalStage)
	if len(post) > 0 {
		image["post"] = post
	}

	return image
}

// createSymlinks creates symlink configuration for binaries
func createSymlinks(stage *parser.Stage) map[string]interface{} {
	post := make(map[string]interface{})
	symlinks := make(map[string]interface{})

	for _, copy := range stage.Copies {
		if copy.From == "builder" || strings.Contains(copy.From, "build") {
			for _, src := range copy.Source {
				if strings.Contains(src, "/bin/") && strings.Contains(copy.Dest, "/usr/local/bin/") {
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
		}
	}

	if len(symlinks) > 0 {
		post["symlinks"] = symlinks
	}

	return post
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

func findBuilderStageName(info *parser.DockerfileInfo) string {
	for _, stage := range info.Stages {
		if isBuilderStage(stage) {
			if stage.Name != "" {
				return stage.Name
			}
		}
	}
	return "builder"
}

func getArgValueOrDefault(info *parser.DockerfileInfo, key string, defaultValue any) any {
	if info == nil {
		return fmt.Sprintf("%v", defaultValue)
	}

	if val, exists := info.Args[key]; exists && val != "" {
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
