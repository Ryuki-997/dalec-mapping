package transformer

import (
	"dalec/parser"
	"fmt"
	"strings"

	"dalec/github"
)

// extractDependencies extracts build and runtime dependencies
func extractDependencies(defaultSpec *DefaultSpec) map[string]interface{} {
	deps := make(map[string]interface{})
	buildDeps := make(map[string]interface{})
	runtimeDeps := make(map[string]interface{})

	// Iterate through all stages to extract dependencies
	for _, stage := range defaultSpec.Stages {
		isBuilder := isBuilderStage(stage)

		// TODO: Add language-specific default dependencies (rust, python)
		if defaultSpec.Generator == github.GoModGenerator {
			buildDeps["msft-golang"] = map[string]interface{}{}
		}

		// Parse RUN commands for package installations
		for _, run := range stage.Runs {
			packages := extractPackagesFromRun(run)

			fmt.Println("Extracted packages from RUN:", packages)

			for _, pkg := range packages {
				pkgDef := make(map[string]interface{})

				// Add version constraint if specified
				if pkg.Version != "" {
					pkgDef["version"] = []string{pkg.Version}
				}

				// If no fields, use empty map
				if len(pkgDef) == 0 {
					pkgDef = map[string]interface{}{}
				}

				// Assign to build or runtime deps based on stage type
				if isBuilder {
					buildDeps[pkg.Name] = pkgDef
				} else {
					runtimeDeps[pkg.Name] = pkgDef
				}
			}
		}
	}

	// Ensure build & runtime dependencies are included
	deps["build"] = buildDeps
	deps["runtime"] = runtimeDeps

	return deps
}

// ///////////////////////////////////////////////////
// /// Helper functions for dependency extraction  ///
// ///////////////////////////////////////////////////

// Package represents a parsed package with optional version
type Package struct {
	Name    string
	Version string
}

// extractPackagesFromRun parses RUN commands to extract package names and versions
func extractPackagesFromRun(cmd string) []Package {
	fmt.Println("Parsing RUN command for packages:", cmd)
	cmd = strings.TrimSpace(cmd)
	cmdLower := strings.ToLower(cmd)

	// Check for different package managers
	if strings.Contains(cmdLower, "apt-get install") || strings.Contains(cmdLower, "apt install") {
		return parseAptPackages(cmd)
	}
	if strings.Contains(cmdLower, "yum install") {
		return parseYumPackages(cmd)
	}
	if strings.Contains(cmdLower, "dnf install") {
		return parseDnfPackages(cmd)
	}
	if strings.Contains(cmdLower, "apk add") {
		return parseApkPackages(cmd)
	}
	if strings.Contains(cmdLower, "tdnf install") {
		return parseTdnfPackages(cmd)
	}

	return []Package{}
}

// parseAptPackages parses apt-get/apt install commands
// Format: apt-get install package1 package2=version package3
func parseAptPackages(cmd string) []Package {
	packages := []Package{}

	// Find the install command and get everything after it
	installIdx := strings.Index(strings.ToLower(cmd), "install")
	if installIdx == -1 {
		return packages
	}

	// Get the part after "install"
	afterInstall := cmd[installIdx+7:] // len("install") = 7
	words := strings.Fields(afterInstall)

	for _, word := range words {
		// Skip flags and options
		if strings.HasPrefix(word, "-") {
			continue
		}
		// Skip common apt flags
		if word == "update" || word == "upgrade" || word == "&&" || word == "||" {
			continue
		}

		// Check for version specification (package=version)
		if strings.Contains(word, "=") {
			parts := strings.SplitN(word, "=", 2)
			packages = append(packages, Package{
				Name:    parts[0],
				Version: parts[1],
			})
		} else if isValidPackageName(word) {
			packages = append(packages, Package{Name: word})
		}
	}

	return packages
}

// parseYumPackages parses yum install commands
func parseYumPackages(cmd string) []Package {
	return parseRpmBasedPackages(cmd, "install")
}

// parseDnfPackages parses dnf install commands
func parseDnfPackages(cmd string) []Package {
	return parseRpmBasedPackages(cmd, "install")
}

// parseTdnfPackages parses tdnf install commands
func parseTdnfPackages(cmd string) []Package {
	return parseRpmBasedPackages(cmd, "install")
}

// parseRpmBasedPackages handles yum/dnf/tdnf package parsing
// Format: yum install package1 package2-version package3
func parseRpmBasedPackages(cmd, keyword string) []Package {
	packages := []Package{}

	installIdx := strings.Index(strings.ToLower(cmd), keyword)
	if installIdx == -1 {
		return packages
	}

	afterInstall := cmd[installIdx+len(keyword):]
	words := strings.Fields(afterInstall)

	for _, word := range words {
		// Skip flags
		if strings.HasPrefix(word, "-") && len(word) == 2 {
			continue
		}
		// Skip common keywords
		if word == "&&" || word == "||" || word == "update" || word == "upgrade" {
			continue
		}

		if isValidPackageName(word) {
			packages = append(packages, Package{Name: word})
		}
	}

	return packages
}

// parseApkPackages parses apk add commands
// Format: apk add package1 package2=version package3~=version
func parseApkPackages(cmd string) []Package {
	packages := []Package{}

	addIdx := strings.Index(strings.ToLower(cmd), "add")
	if addIdx == -1 {
		return packages
	}

	afterAdd := cmd[addIdx+3:] // len("add") = 3
	words := strings.Fields(afterAdd)

	for _, word := range words {
		// Skip flags
		if strings.HasPrefix(word, "-") {
			continue
		}
		// Skip common keywords
		if word == "&&" || word == "||" {
			continue
		}

		// Check for version specifications
		if strings.Contains(word, "=") {
			parts := strings.SplitN(word, "=", 2)
			packages = append(packages, Package{
				Name:    parts[0],
				Version: parts[1],
			})
		} else if strings.Contains(word, "~") {
			parts := strings.SplitN(word, "~", 2)
			packages = append(packages, Package{
				Name:    parts[0],
				Version: "~" + parts[1],
			})
		} else if isValidPackageName(word) {
			packages = append(packages, Package{Name: word})
		}
	}

	return packages
}

// isValidPackageName checks if a string looks like a valid package name
func isValidPackageName(s string) bool {
	// Basic heuristic: not empty, not a common shell operator
	if s == "" || s == "&&" || s == "||" || s == ";" {
		return false
	}
	// Should not start with special characters (except allowed in package names)
	if strings.HasPrefix(s, ">") || strings.HasPrefix(s, "<") || strings.HasPrefix(s, "|") {
		return false
	}
	// Should contain alphanumeric characters
	hasAlpha := false
	for _, ch := range s {
		if (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') {
			hasAlpha = true
			break
		}
	}
	return hasAlpha
}

// isBuilderStage determines if a stage is a builder stage
func isBuilderStage(stage parser.Stage) bool {
	// Check stage name
	if stage.Name != "" {
		nameLower := strings.ToLower(stage.Name)
		if strings.Contains(nameLower, "build") {
			return true
		}
	}

	// Check if it has build commands
	for _, run := range stage.Runs {
		runLower := strings.ToLower(run)
		if strings.Contains(runLower, "go build") ||
			strings.Contains(runLower, "cargo build") ||
			strings.Contains(runLower, "npm run build") ||
			strings.Contains(runLower, "make") ||
			strings.Contains(runLower, "cmake") ||
			strings.Contains(runLower, "gcc") ||
			strings.Contains(runLower, "g++") {
			return true
		}
	}

	return false
}
