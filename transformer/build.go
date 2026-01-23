package transformer

import (
	"dalec/parser"
	"fmt"
	"strings"
)

// extractBuildSteps converts RUN commands to Dalec build steps (uses nonDeterministicValues if available)
func extractBuildSection(defaultSpec *DefaultSpec, nonDeterministicValues *parser.NonDeterministicValues) (map[string]interface{}, string) {
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
	if nonDeterministicValues != nil && nonDeterministicValues.LdFlags != "" {
		env["LDFLAGS"] = nonDeterministicValues.LdFlags
	}

	build["env"] = env

	// Extract build steps
	command, output := extractBuildSteps(nonDeterministicValues)

	buildCommand := "cd " + defaultSpec.Repo + "\n"

	fmt.Printf("COMMAND: %v\n", command)
	if command == "" {
		// Default: cd to repo and run go build
		output := fmt.Sprintf("bin/%s", defaultSpec.Repo)
		buildCommand += fmt.Sprintf("cd %s\ngo build -o %s ./main.go", defaultSpec.Repo, output)
	} else {
		buildCommand += command
	}

	fmt.Printf("OUTPUT: %v\n", output)

	steps := []map[string]interface{}{
		{"command": buildCommand},
	}

	build["steps"] = steps

	return build, output
}

func extractBuildSteps(nonDeterministicValues *parser.NonDeterministicValues) (string, string) {
	command := ""
	output := ""

	if nonDeterministicValues == nil {
		return command, output
	}

	output = extractOutput(nonDeterministicValues)

	// Build primary binary
	if nonDeterministicValues.BuildCommand != "" {
		// Use the explicit build command if provided
		command += nonDeterministicValues.BuildCommand
	} else if nonDeterministicValues.LdFlags != "" {
		// Construct build command from ldflags
		command += "go build -ldflags \"" + nonDeterministicValues.LdFlags + "\" -o " + output + " ./pkg/" + nonDeterministicValues.BinaryName
	}

	// Build auxiliary binaries
	for _, aux := range nonDeterministicValues.AuxiliaryBinaries {
		if aux.Name == "" {
			continue
		}

		// Always output to simple binary name for Dalec artifacts compatibility
		auxOutput := aux.Name

		if aux.BuildCommand != "" {
			// Use explicit build command if provided
			command += "\n" + aux.BuildCommand
		} else if aux.LdFlags != "" {
			command += "\ngo build -ldflags \"" + aux.LdFlags + "\" -o " + auxOutput + " ./pkg/" + aux.Name
		} else {
			command += "\ngo build -o " + auxOutput + " ./pkg/" + aux.Name
		}
	}

	return command, output
}

func extractOutput(nonDeterministicValues *parser.NonDeterministicValues) string {
	output := ""
	if nonDeterministicValues == nil {
		return output
	}

	command := nonDeterministicValues.BuildCommand
	parts := strings.Split(command, " ")

	for i, part := range parts {
		if part == "-o" && i+1 < len(parts) {
			output = parts[i+1]
			break
		}
	}

	return output
}
