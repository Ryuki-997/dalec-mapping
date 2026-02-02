package tool

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// HELPER FUNCTIONS

// getGeneratorPath returns the absolute path to the generator directory
func getGeneratorPath() (string, error) {
	// Try relative path first
	if _, err := os.Stat(GeneratorDir); err == nil {
		absPath, _ := filepath.Abs(GeneratorDir)
		return absPath, nil
	}

	// Try from current working directory
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}

	genPath := filepath.Join(wd, GeneratorDir)
	if _, err := os.Stat(genPath); err == nil {
		return genPath, nil
	}

	// Try parent directory (in case running from subdirectory)
	parentGen := filepath.Join(wd, "..", GeneratorDir)
	if _, err := os.Stat(parentGen); err == nil {
		absPath, _ := filepath.Abs(parentGen)
		return absPath, nil
	}

	return "", fmt.Errorf("generator directory not found (tried: %s, %s, %s)",
		GeneratorDir, genPath, parentGen)
}

// extractRepoName extracts the repo name from owner/repo format
func extractRepoName(repo string) string {
	parts := strings.Split(repo, "/")
	if len(parts) >= 2 {
		return parts[1]
	}
	return repo
}

// parseFilepathYmlArrays parses filepath.yml and returns arrays of full paths
func parseFilepathYmlArrays(content string, resultPath string) (dockerfiles, makefiles []string) {
	lines := strings.Split(content, "\n")
	inDockerfiles := false
	inMakefiles := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		if trimmed == "dockerfiles:" {
			inDockerfiles = true
			inMakefiles = false
			continue
		}
		if trimmed == "makefiles:" {
			inDockerfiles = false
			inMakefiles = true
			continue
		}

		if strings.HasPrefix(trimmed, "- ") {
			path := strings.TrimPrefix(trimmed, "- ")
			// Remove quotes if present
			path = strings.Trim(path, "\"'")
			fullPath := filepath.Join(resultPath, path)
			if inDockerfiles {
				dockerfiles = append(dockerfiles, fullPath)
			}
			if inMakefiles {
				makefiles = append(makefiles, fullPath)
			}
		}
	}

	return dockerfiles, makefiles
}