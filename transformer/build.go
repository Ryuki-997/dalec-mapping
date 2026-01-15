package transformer

import (
	"fmt"
	"strings"
)

// extractBuildSteps converts RUN commands to Dalec build steps
func extractBuildSteps(defaultSpec *DefaultSpec) map[string]interface{} {
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
	steps := extractBuildCommands(defaultSpec)

	if len(steps) == 0 {
		buildCommand := fmt.Sprintf("cd %s\ngo build -o bin/%s ./main.go", defaultSpec.Repo, defaultSpec.Repo)
		steps = []map[string]interface{}{
			{"command": buildCommand},
		}
	}

	build["steps"] = steps

	return build
}

// extractBuildCommands extracts build commands from builder stages
func extractBuildCommands(defaultSpec *DefaultSpec) []map[string]interface{} {
	var steps []map[string]interface{}

	for _, stage := range defaultSpec.Stages {

		if len(stage.Runs) == 0 {
			continue
		}

		// Combine relevant build commands
		var commands []string
		for _, run := range stage.Runs {
			// Filter out package installations (they go in dependencies)
			if isPackageInstallCommand(run) {
				continue
			}

			cleanedCommand := cleanInlineEnvVars(run)
			commands = append(commands, cleanedCommand)

			if len(commands) == 0 {
				continue
			}

			cmd := strings.Join(commands, "\n")

			// Adjust working directory
			if stage.Workdir != "" {
				cmd = adjustWorkDir(cmd, stage.Workdir, defaultSpec.Repo)
			}

			steps = append(steps, map[string]interface{}{
				"command": cmd,
			})
		}
	}

	return steps
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

func adjustWorkDir(command, workdir, repoName string) string {
	command = strings.ReplaceAll(command, "cd "+workdir, "cd "+repoName)
	return command
}
