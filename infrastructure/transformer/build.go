package transformer

import (
	"fmt"
	"strings"

	"dalec-mapping/domain/contents"
	"dalec-mapping/domain/llm"
	"dalec-mapping/infrastructure/github"
)

// extractBuildSteps converts RUN commands to Dalec build steps (uses nonDeterministicValues if available)
func extractBuildSection(defaultSpec *contents.DefaultSpec, nonDeterministicValues *llm.NonDeterministicValues) map[string]interface{} {
	build := make(map[string]interface{})
	env := make(map[string]interface{})

	// Add standard env vars (deterministic)
	env["GOPROXY"] = "direct"
	env["GOEXPERIMENT"] = "systemcrypto"
	// env["CGO_ENABLED"] = "1"
	env["VERSION"] = "${VERSION}"

	// Set CGO_ENABLED: use LLM/parser value if provided, default to "1" for FIPS compliance
	cgoEnabled := "1"
	if nonDeterministicValues != nil && nonDeterministicValues.CgoEnabled != "" {
		cgoEnabled = nonDeterministicValues.CgoEnabled
	}
	env["CGO_ENABLED"] = cgoEnabled

	skipArguments := map[string]bool{
		"COMMIT":     true,
		"VERSION":    true,
		"REVISION":   true,
		"ARCH":       true,
		"OS":         true,
		"OS_VERSION": true,
		"GOARCH":     true,
		"GOOS":       true,
	}

	for arg := range defaultSpec.Args {
		if skipArguments[arg] {
			continue
		}
		env[string(arg)] = fmt.Sprintf("${%s}", arg)
	}

	// Add LDFLAGS from NonDeterministicValues if available
	if nonDeterministicValues != nil {
		for _, aux := range nonDeterministicValues.Binaries {
			if aux.LdFlags != "" {
				env["LDFLAGS"] = aux.LdFlags
				break
			}
		}
	}

	build["env"] = env

	// Extract build steps
	command := extractBuildSteps(nonDeterministicValues, defaultSpec.Repo)

	buildCommand := "cd " + defaultSpec.Repo + "\n"

	fmt.Printf("COMMAND: %v\n", command)
	if command == "" {
		// Default: cd to repo and run go build
		output := fmt.Sprintf("bin/%s", defaultSpec.Repo)
		buildCommand += fmt.Sprintf("go build -o %s ./main.go", output)
	} else {
		buildCommand += command
	}

	fmt.Printf("BUILD COMMAND: %v\n", buildCommand)

	steps := []map[string]interface{}{
		{"command": buildCommand},
	}

	build["steps"] = steps

	return build
}

func extractBuildSteps(nonDeterministicValues *llm.NonDeterministicValues, repoName string) string {
	command := ""

	if nonDeterministicValues == nil {
		return command
	}

	// Build additional binaries
	for _, aux := range nonDeterministicValues.Binaries {
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
