package github

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

type GitHubCrawler struct{}

// FileSearchResult contains paths to Dockerfiles and Makefiles found in the repo
type FileSearchResult struct {
	Dockerfiles []string `yaml:"dockerfiles"`
	Makefiles   []string `yaml:"makefiles"`
}

// DFS on a GitHub repository to find all Dockerfiles and Makefiles
func FindBuildFiles(result *FileSearchResult, owner, repo, branch string) (*FileSearchResult, error) {
	if result == nil {
		return nil, fmt.Errorf("Error: Struct should be passed from main.")
	}

	// Handle branch with subdirectory (e.g., "master/addon-resizer")
	actualBranch := branch
	if strings.Contains(branch, "/") {
		parts := strings.SplitN(branch, "/", 2)
		actualBranch = parts[0]
	}

	fmt.Printf("🔍 Searching repository %s/%s (branch: %s) for build files...\n", owner, repo, actualBranch)

	err := dfsDirectory(owner, repo, actualBranch, "", result)
	if err != nil {
		return nil, fmt.Errorf("failed to search repository: %w", err)
	}

	fmt.Printf("✅ Found %d Dockerfile(s) and %d Makefile(s)\n", len(result.Dockerfiles), len(result.Makefiles))

	return result, nil
}

// WriteYAML writes the FileSearchResult to filepath.yml in the result directory
func (w *GitHubCrawler) WriteYAML(result *FileSearchResult, resultDir string) error {
	// Ensure result directory exists
	if err := os.MkdirAll(resultDir, 0755); err != nil {
		return fmt.Errorf("failed to create result directory: %w", err)
	}

	filepathPath := filepath.Join(resultDir, "filepath.yml")

	yamlData, err := yaml.Marshal(result)
	if err != nil {
		return fmt.Errorf("failed to marshal filepath.yml: %w", err)
	}

	err = os.WriteFile(filepathPath, yamlData, 0644)
	if err != nil {
		return fmt.Errorf("failed to write filepath.yml: %w", err)
	}

	fmt.Printf("📄 Wrote filepath.yml to %s\n", filepathPath)
	return nil
}

// ReadYAML reads the FileSearchResult from filepath.yml
func (w *GitHubCrawler) ReadYAML(resultDir string) (*FileSearchResult, error) {
	filepathPath := filepath.Join(resultDir, "filepath.yml")

	content, err := os.ReadFile(filepathPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read filepath.yml: %w", err)
	}

	var result FileSearchResult
	err = yaml.Unmarshal(content, &result)
	if err != nil {
		return nil, fmt.Errorf("failed to parse filepath.yml: %w", err)
	}

	return &result, nil
}

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

// dfsDirectory recursively searches a directory for Dockerfiles and Makefiles
func dfsDirectory(owner, repo, branch, path string, result *FileSearchResult) error {
	// Build API URL for directory contents
	var url string
	if path == "" {
		url = fmt.Sprintf("https://api.github.com/repos/%s/%s/contents?ref=%s", owner, repo, branch)
	} else {
		url = fmt.Sprintf("https://api.github.com/repos/%s/%s/contents/%s?ref=%s", owner, repo, path, branch)
	}

	data, err := makeGitHubArrayRequest(url)
	if err != nil {
		// Only log error for root directory, silently skip subdirectories
		if path == "" {
			fmt.Printf("  ❌ Error fetching root directory: %v\n", err)
		}
		return nil
	}

	for _, item := range data {
		itemMap, ok := item.(map[string]interface{})
		if !ok {
			continue
		}

		name, ok := itemMap["name"].(string)
		if !ok {
			continue
		}

		itemType, _ := itemMap["type"].(string)
		itemPath, _ := itemMap["path"].(string)

		switch itemType {
		case "file":
			lowerName := strings.ToLower(name)
			if lowerName == "dockerfile" || strings.HasSuffix(lowerName, ".dockerfile") {
				result.Dockerfiles = append(result.Dockerfiles, itemPath)
				fmt.Printf("  📄 Found Dockerfile: %s\n", itemPath)
			} else if lowerName == "makefile" {
				result.Makefiles = append(result.Makefiles, itemPath)
				fmt.Printf("  📄 Found Makefile: %s\n", itemPath)
			}
		case "dir":
			// Skip common directories that won't have useful build files
			skipDirs := map[string]bool{
				"vendor":       true,
				"node_modules": true,
				".git":         true,
				".github":      true,
				"testdata":     true,
				"test":         true,
				"tests":        true,
				"examples":     true,
				"docs":         true,
				"doc":          true,
				"charts":       true,
				"hack":         true,
				"scripts":      true,
			}

			if !skipDirs[name] {
				// Recursively search subdirectory
				err := dfsDirectory(owner, repo, branch, itemPath, result)
				if err != nil {
					// Log but continue - don't fail entire search for one directory
					fmt.Printf("  ⚠️  Skipping %s: %v\n", itemPath, err)
				}
			}
		}
	}

	return nil
}
