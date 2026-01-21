package transformer

import (
	"dalec/parser"
	"fmt"
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

	build["env"] = env

	// Extract build steps
	command, output := extractBuildSteps(nonDeterministicValues)

	buildCommand := "cd " + defaultSpec.Repo + "\n"
	if command == "" {
		// Default: cd to repo and run go build
		output := fmt.Sprintf("bin/%s", defaultSpec.Repo)
		buildCommand += fmt.Sprintf("cd %s\ngo build -o %s ./main.go", defaultSpec.Repo, output)
	} else {
		buildCommand += command
	}

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

	if nonDeterministicValues.BuildCommand != "" {
		if nonDeterministicValues.LdFlags != "" {
			output = nonDeterministicValues.BinaryName
			command += "go build -ldflags \"" + nonDeterministicValues.LdFlags + "\" -o " + output + " ./pkg/" + nonDeterministicValues.BinaryName
		} else {
			command += nonDeterministicValues.BuildCommand
		}

	}

	return command, output
}
