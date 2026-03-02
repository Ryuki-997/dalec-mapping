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

	buildCommand := "cd " + defaultSpec.Repo + "\n"
	if command == "" {
		output := fmt.Sprintf("bin/%s", defaultSpec.Repo)
		buildCommand += fmt.Sprintf("go build -o %s ./main.go", output)
	} else {
		buildCommand += command
	}

	// Collect all text to scan for variable references:
	// build commands + LdFlags from env
	var scanTexts []string
	scanTexts = append(scanTexts, buildCommand)
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

	steps := []map[string]interface{}{
		{"command": buildCommand},
	}

	build["steps"] = steps

	return build, referencedVars
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
