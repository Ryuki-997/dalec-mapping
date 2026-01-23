package github

import (
	"fmt"
	"strings"
)

// FileSearchResult contains paths to Dockerfiles and Makefiles found in the repo
type FileSearchResult struct {
	Dockerfiles []string
	Makefiles   []string
}

// DFS on a GitHub repository to find all Dockerfiles and Makefiles
func FindBuildFiles(result *FileSearchResult, owner, repo, branch string) (*FileSearchResult, error) {
	if result == nil {
		return nil, fmt.Errorf("Error: Struct should be passed from main.")
	}

	err := dfsDirectory(owner, repo, branch, "", result)
	if err != nil {
		return nil, fmt.Errorf("failed to search repository: %w", err)
	}

	return result, nil
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
				"testdata":     true,
				"test":         true,
				"tests":        true,
				"examples":     true,
				"docs":         true,
				"doc":          true,
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
