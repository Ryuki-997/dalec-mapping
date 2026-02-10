package transformer

import (
	"fmt"

	"dalec-mapping/global"
)

// extractBuildSteps converts RUN commands to Dalec build steps (uses nonDeterministicValues if available)
func extractBuildSection(defaultSpec *DefaultSpec, nonDeterministicValues *global.NonDeterministicValues) map[string]interface{} {
	build := make(map[string]interface{})
	env := make(map[string]interface{})

	// Add standard env vars (deterministic)
	env["GOPROXY"] = "direct"
	env["GOEXPERIMENT"] = "systemcrypto"
	env["CGO_ENABLED"] = "1"
	env["VERSION"] = "${VERSION}"

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
	command := extractBuildSteps(nonDeterministicValues)

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

func extractBuildSteps(nonDeterministicValues *global.NonDeterministicValues) string {
	command := ""

	if nonDeterministicValues == nil {
		return command
	}

	// Build additional binaries
	for _, aux := range nonDeterministicValues.Binaries {
		if aux.Name == "" {
			continue
		}

		if aux.BuildCommand != "" {
			// Use explicit build command if provided
			command += "\n" + aux.BuildCommand
			fmt.Printf("Current Command: %v\n", command)
		} else if aux.LdFlags != "" {
			command += "\ngo build -ldflags \"" + aux.LdFlags + "\" -o " + aux.OutputPath + " ./pkg/" + aux.Name
		} else {
			command += "\ngo build -o " + aux.OutputPath + " ./pkg/" + aux.Name
		}
	}

	return command
}
