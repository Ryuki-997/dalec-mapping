package github

// ─── Chunk 3 · REPOSITORY INFO ──────────────────────────────────────────────

import (
	"fmt"
	"log"
	"os"
	"path"
	"strings"

	"dalec-mapping/domain/repository"
)

// FetchRepoInfo fetches repository metadata from the GitHub API.
// configuredLicense overrides the license detected from the GitHub API when non-empty.
func FetchRepoInfo(repoPath, configuredLicense string) (*repository.RepoInfo, error) {
	baseRef, componentPath := SplitGitHubComponent(repoPath)
	owner, repo, branch := fetchRepositorySegments(baseRef)

	componentName := ""
	if componentPath != "" {
		componentName = path.Base(componentPath)
	}

	info := &repository.RepoInfo{
		Owner:         owner,
		Repo:          repo,
		Branch:        branch,
		ComponentPath: componentPath,
		ComponentName: componentName,
		GitURL:        fmt.Sprintf("https://github.com/%s/%s", owner, repo),
	}

	if err := fetchRepoMetadata(info, configuredLicense); err != nil {
		return nil, fmt.Errorf("failed to fetch repo metadata: %w", err)
	}
	if err := fetchSourceGenerator(info); err != nil {
		return nil, fmt.Errorf("failed to fetch source generator: %w", err)
	}
	return info, nil
}

// fetchRepositorySegments extracts the repository segments from a GitHub URL or path.
func fetchRepositorySegments(repo string) (owner, name, branch string) {
	repo = strings.TrimSuffix(repo, ".git")
	repo = strings.TrimPrefix(repo, "https://")
	repo = strings.TrimPrefix(repo, "http://")
	repo = strings.TrimPrefix(repo, "github.com/")

	parts := strings.Split(repo, "/")
	if len(parts) == 2 {
		repoData, err := FetchJSON(fmt.Sprintf("repos/%s/%s", parts[0], parts[1]))
		if err != nil {
			log.Printf("Error: failed to fetch repo info for %s: %v\n", repo, err)
			os.Exit(1)
		}

		branch := "main"
		if defaultBranch, ok := repoData["default_branch"].(string); ok && defaultBranch != "" {
			branch = defaultBranch
		}

		return parts[0], parts[1], branch
	} else if len(parts) >= 4 && parts[2] == "tree" {
		return parts[0], parts[1], parts[3]
	}

	log.Printf("Warning: unrecognized repository format: %s\n", repo)
	os.Exit(1)
	return "", "", ""
}
