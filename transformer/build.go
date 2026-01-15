package transformer

import (
	"fmt"
	"strings"
)

// extractBuildSteps converts RUN commands to Dalec build steps
func extractBuildSteps(defaultSpec *DefaultSpec) (map[string]interface{}, string) {
	build := make(map[string]interface{})

	// Extract environment variables
	env := make(map[string]string)
	env["VERSION"] = "${VERSION}"
	env["CGO_ENABLED"] = "1"
	// env["GOOS"] = "${OS}"
	env["GOPROXY"] = "direct"
	env["GOEXPERIMENT"] = "systemcrypto"

	skipArguments := map[string]bool{
		"COMMIT":     true,
		"VERSION":    true,
		"REVISION":   true,
		"ARCH":       true,
		"OS":         true,
		"OS_VERSION": true,
	}

	for arg := range defaultSpec.Args {
		if skipArguments[arg] {
			continue
		}
		env[string(arg)] = fmt.Sprintf("${%s}", arg)
	}

	build["env"] = env

	// Extract build steps
	commands := extractBuildCommands(defaultSpec)

	var buildCommand string
	var output string
	if len(commands) == 0 {
		// Default: cd to repo and run go build
		output = fmt.Sprintf("bin/%s", defaultSpec.Repo)
		buildCommand = fmt.Sprintf("cd %s\ngo build -o %s ./main.go", defaultSpec.Repo, output)
	} else {
		// Use extracted commands with cd prefix
		output = getBinaryOutput(defaultSpec.Repo, commands)
		buildCommand = fmt.Sprintf("cd %s\n%s", defaultSpec.Repo, strings.Join(commands, "\n"))
	}

	steps := []map[string]interface{}{
		{"command": buildCommand},
	}

	build["steps"] = steps

	return build, output
}

// extractBuildCommands extracts build commands from builder stages
func extractBuildCommands(defaultSpec *DefaultSpec) []string {
	var commands []string

	for _, stage := range defaultSpec.Stages {
		if len(stage.Runs) == 0 {
			continue
		}

		// Collect relevant build commands from this stage
		for _, run := range stage.Runs {
			// Filter out package installations (they go in dependencies)
			if isPackageInstallCommand(run) {
				continue
			}

			cleanedCommand := cleanInlineEnvVars(run)

			if cleanedCommand != "" {
				commands = append(commands, cleanedCommand)
			}
		}
	}

	return commands
}

func isPackageInstallCommand(command string) bool {
	run := strings.ToLower(command)
	installKeywords := []string{"apt-get", "yum", "dnf", "apk", "tdnf"}

	for _, keyword := range installKeywords {
		if strings.Contains(run, keyword) {
			return true
		}
	}
	return false
}

func cleanInlineEnvVars(command string) string {
	command = strings.ReplaceAll(command, "CGO_ENABLED=0 ", "")
	command = strings.ReplaceAll(command, "CGO_ENABLED=1 ", "")
	command = strings.ReplaceAll(command, "GOOS=$OS ", "")
	return command
}

// getBinaryOutput extracts the binary output path from build commands
func getBinaryOutput(repoName string, commands []string) string {
	for _, cmd := range commands {
		if !strings.Contains(cmd, "go build") {
			continue
		}

		// Find -o flag and extract the path
		fields := strings.Fields(cmd)
		for i, field := range fields {
			if field != "-o" || i+1 >= len(fields) {
				continue
			}

			outputPath := fields[i+1]
			return outputPath
		}
	}

	return fmt.Sprintf("bin/%s", repoName)
}
