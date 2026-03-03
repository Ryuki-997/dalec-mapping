package transformer

import (
	"fmt"
	"regexp"
	"strings"

	"dalec-mapping/domain/contents"
	"dalec-mapping/domain/llm"
	"dalec-mapping/infrastructure/github"
)

// extractBuildSection converts RUN commands to Dalec build steps (uses nonDeterministicValues if available).
// It returns the build map and a set of variable names referenced in the build command/ldflags.
func extractBuildSection(defaultSpec *contents.DefaultSpec, makefileInfo *contents.MakefileInfo, nonDeterministicValues *llm.NonDeterministicValues) (map[string]interface{}, map[string]bool) {
	build := make(map[string]interface{})
	env := make(map[string]interface{})

	// Add standard env vars (deterministic)
	env["GOPROXY"] = "direct"
	env["GOEXPERIMENT"] = "systemcrypto"
	env["VERSION"] = "${VERSION}"

	// Set CGO_ENABLED: GOEXPERIMENT=systemcrypto requires CGO_ENABLED=1 for FIPS compliance.
	// Always force "1" since we always set systemcrypto above.
	env["CGO_ENABLED"] = "1"

	// Extract build steps first so we can scan them for variable references
	command := extractBuildSteps(nonDeterministicValues, defaultSpec.Repo)

	// Add LDFLAGS from NonDeterministicValues if available
	if nonDeterministicValues != nil {
		for _, aux := range nonDeterministicValues.Binaries {
			if aux.LdFlags != "" {
				env["LDFLAGS"] = aux.LdFlags
				break
			}
		}
	}

	// Build the full command string used for variable scanning
	var scanCommand string
	if command == "" {
		output := fmt.Sprintf("bin/%s", defaultSpec.Repo)
		scanCommand = fmt.Sprintf("cd %s\ngo build -o %s ./main.go", defaultSpec.Repo, output)
	} else {
		scanCommand = "cd " + defaultSpec.Repo + "\n" + command
	}

	// Collect all text to scan for variable references:
	// build commands + LdFlags from env
	var scanTexts []string
	scanTexts = append(scanTexts, scanCommand)
	if ldflags, ok := env["LDFLAGS"]; ok {
		scanTexts = append(scanTexts, fmt.Sprintf("%v", ldflags))
	}

	// Find all ${VAR} and $(VAR) references in build commands and LdFlags
	varRefPattern := regexp.MustCompile(`\$[{(]([A-Za-z_][A-Za-z0-9_]*)[})]`)
	referencedVars := make(map[string]bool)
	for _, text := range scanTexts {
		for _, match := range varRefPattern.FindAllStringSubmatch(text, -1) {
			referencedVars[match[1]] = true
		}
	}

	// Add referenced Makefile variables to env as ${VAR} references
	if makefileInfo != nil {
		for varName := range referencedVars {
			if _, alreadySet := env[varName]; alreadySet {
				continue
			}
			if _, exists := makefileInfo.Variables[varName]; exists {
				env[varName] = fmt.Sprintf("${%s}", varName)
			}
		}
	}

	build["env"] = env

	// Generate per-binary steps — merge "cd X &&" into the initial "cd repo/X"
	var steps []map[string]interface{}
	if command == "" {
		steps = append(steps, map[string]interface{}{"command": scanCommand})
	} else {
		for _, line := range strings.Split(strings.TrimSpace(command), "\n") {
			if line == "" {
				continue
			}
			subdir, stripped := extractCdDir(line)
			var stepCmd string
			if subdir != "" {
				stepCmd = fmt.Sprintf("cd %s/%s\n%s", defaultSpec.Repo, subdir, stripped)
			} else {
				stepCmd = fmt.Sprintf("cd %s\n%s", defaultSpec.Repo, line)
			}
			steps = append(steps, map[string]interface{}{"command": stepCmd})
		}
	}

	build["steps"] = steps

	return build, referencedVars
}

// extractCdDir detects a leading "cd X &&" in a single build command line.
// Returns the directory (X) and the remaining command with the cd prefix stripped.
// If no such prefix exists, returns ("", original line).
func extractCdDir(line string) (subdir, stripped string) {
	line = strings.TrimSpace(line)
	cdPattern := regexp.MustCompile(`^cd\s+(\S+)\s*&&\s*(.+)$`)
	if m := cdPattern.FindStringSubmatch(line); m != nil {
		return m[1], strings.TrimSpace(m[2])
	}
	return "", line
}

func extractBuildSteps(nonDeterministicValues *llm.NonDeterministicValues, repoName string) string {
	command := ""

	if nonDeterministicValues == nil {
		return command
	}

	// Build additional binaries
	for i := range nonDeterministicValues.Binaries {
		aux := &nonDeterministicValues.Binaries[i]
		if aux.Name == "" {
			continue
		}

		// Order matters!!
		github.ClearEnvVariables("LdFlags", &aux.LdFlags)
		github.ClearEnvVariables("OutputPath", &aux.OutputPath)
		github.ClearEnvVariables("BuildCommand", &aux.BuildCommand)

		if aux.BuildCommand != "" {
			command += "\n" + aux.BuildCommand
			fmt.Printf("Current Command: %v\n", command)
		} else if aux.LdFlags != "" {
			// Format output path as reponame/bin/binaryname
			outputPath := aux.OutputPath
			if outputPath == "" {
				outputPath = aux.Name
			}
			if !strings.Contains(outputPath, "/") {
				outputPath = fmt.Sprintf("%s/bin/%s", repoName, outputPath)
			}
			command += "\ngo build -ldflags \"" + aux.LdFlags + "\" -o " + outputPath + " ./pkg/" + aux.Name
		} 
	}

	return command
}
