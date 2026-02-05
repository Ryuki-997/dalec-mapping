package tool

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

const OnboardFilePath = "onboard.yml"

type OnboardingInfo struct {
	Repository string `yaml:"repository"`
}

// ParseOnboardFile reads and parses the onboard.yml file
func ParseOnboardFile(path string, info *OnboardingInfo) error {
	content, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("failed to read onboard file %s: %w", path, err)
	}

	if err := yaml.Unmarshal(content, info); err != nil {
		return fmt.Errorf("failed to parse onboard file: %w", err)
	}

	if info.Repository == "" {
		return fmt.Errorf("repository field is empty in onboard file")
	}

	return nil
}

// getGeneratorPath returns the absolute path to the generator directory
func getGeneratorPath() (string, error) {
	// Get current working directory
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("failed to get working directory: %w", err)
	}

	// Try relative to cwd
	generatorPath := filepath.Join(cwd, "generator")
	if info, err := os.Stat(generatorPath); err == nil && info.IsDir() {
		absPath, _ := filepath.Abs(generatorPath)
		return absPath, nil
	}

	// Try if we're already in the generator directory
	if info, err := os.Stat(filepath.Join(cwd, "main.go")); err == nil && !info.IsDir() {
		// Check if this looks like the generator directory
		if _, err := os.Stat(filepath.Join(cwd, "cli")); err == nil {
			absPath, _ := filepath.Abs(cwd)
			return absPath, nil
		}
	}

	// Try parent directory
	parentPath := filepath.Join(cwd, "..", "generator")
	if info, err := os.Stat(parentPath); err == nil && info.IsDir() {
		absPath, _ := filepath.Abs(parentPath)
		return absPath, nil
	}

	return "", fmt.Errorf("generator directory not found (cwd: %s)", cwd)
}

// extractRepoName extracts the repository name from a GitHub URL or path
func extractRepoName(repo string) string {
	// Remove protocol and domain
	repo = strings.TrimSuffix(repo, ".git")
	repo = strings.TrimPrefix(repo, "https://")
	repo = strings.TrimPrefix(repo, "http://")
	repo = strings.TrimPrefix(repo, "github.com/")

	// Split by / and get repo name (owner/repo -> repo)
	parts := strings.Split(repo, "/")
	if len(parts) >= 2 {
		return parts[1] // Return repo name (second part after owner)
	}
	if len(parts) == 1 {
		return parts[0]
	}
	return repo
}