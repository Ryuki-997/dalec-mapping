package github

import (
	"fmt"
	"os"
)

type GitHubCrawler struct{}

// ClearResultDirectory removes all contents from the result directory
func ClearResultDirectory(resultDir string) error {
	// Check if directory exists
	if _, err := os.Stat(resultDir); os.IsNotExist(err) {
		// Directory doesn't exist, nothing to clear
		return nil
	}

	// Remove all contents
	err := os.RemoveAll(resultDir)
	if err != nil {
		return fmt.Errorf("failed to clear result directory: %w", err)
	}

	fmt.Printf("🗑️  Cleared result directory: %s\n", resultDir)
	return nil
}
