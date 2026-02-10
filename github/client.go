package github

import (
	"fmt"
	"strings"

	"dalec-mapping/global"
)

// FetchRepoInfo fetches repository metadata from GitHub API
func FetchRepoInfo(repoPath string) (*global.RepoInfo, error) {
	owner, repo, branch, _ := global.ExtractRepositorySegments(repoPath)

	fmt.Printf("Parsed - Owner: %s, Repo: %s, Branch: %s\n", owner, repo, branch)

	info := &global.RepoInfo{
		Owner:  owner,
		Repo:   repo,
		Branch: branch,
		GitURL: fmt.Sprintf("https://github.com/%s/%s", owner, repo),
	}

	// Fetch repository metadata
	if err := fetchRepoMetadata(info); err != nil {
		return nil, fmt.Errorf("failed to fetch repo metadata: %w", err)
	}

	// Fetch latest commit
	if err := fetchReleaseMetadata(info); err != nil {
		return nil, fmt.Errorf("failed to fetch latest commit: %w", err)
	}

	// Fetch source generator
	if err := fetchSourceGenerator(info); err != nil {
		return nil, fmt.Errorf("failed to fetch source generator: %w", err)
	}

	return info, nil
}

// Acquire default metadata
func fetchRepoMetadata(info *global.RepoInfo) error {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s", info.Owner, info.Repo)

	data, err := global.MakeGitHubRequest[map[string]interface{}](url)
	if err != nil {
		return err
	}

	// Extract default branch if not already set
	if info.Branch == "" {
		if defaultBranch, ok := data["default_branch"].(string); ok {
			info.Branch = defaultBranch
		} else {
			info.Branch = "main" // Fallback
		}
	}

	// Extract metadata
	if desc, ok := data["description"].(string); ok {
		info.Description = desc
	} else {
		info.Description = fmt.Sprintf("This is the %s project.", info.Repo)
	}

	if url, ok := data["html_url"].(string); ok && url != "" {
		info.GitURL = url
	}

	info.License = "foo-license"
	if license, ok := data["license"].(map[string]interface{}); ok {
		if spdxID, ok := license["spdx_id"].(string); ok && spdxID != "NOASSERTION" {
			info.License = spdxID
		}
	}

	return nil
}

// Acquire release metadata
func fetchReleaseMetadata(info *global.RepoInfo) error {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/latest", info.Owner, info.Repo)

	data, err := global.MakeGitHubRequest[map[string]interface{}](url)
	if err != nil {
		return err
	}

	// Fetch revision from url
	if releaseURL, ok := data["url"].(string); !ok || releaseURL == "" {
		return fmt.Errorf("url not found in response")
	}

	// Fetch version from tag_name
	tag, ok := data["tag_name"].(string)
	if !ok {
		return fmt.Errorf("tag_name not found in response")
	}
	info.Version = tag

	// Fetch the commit SHA for this release tag
	url = fmt.Sprintf("https://api.github.com/repos/%s/%s/commits/%s", info.Owner, info.Repo, tag)
	data, err = global.MakeGitHubRequest[map[string]interface{}](url)
	if err != nil {
		return err
	}

	if sha, ok := data["sha"].(string); ok {
		info.LatestCommit = sha
	}

	return nil
}

func fetchSourceGenerator(info *global.RepoInfo) error {
	// Check if branch contains a subdirectory path (e.g., "master/addon-resizer")
	subdir := ""
	if strings.Contains(info.Branch, "/") {
		parts := strings.SplitN(info.Branch, "/", 2)
		if len(parts) == 2 {
			subdir = parts[1]
		}
	}

	// Build URL - check subdirectory first if specified, then fall back to root
	var url string
	if subdir != "" {
		url = fmt.Sprintf("https://api.github.com/repos/%s/%s/contents/%s", info.Owner, info.Repo, subdir)
	} else {
		url = fmt.Sprintf("https://api.github.com/repos/%s/%s/contents", info.Owner, info.Repo)
	}

	// Verified files susceptible for upstream generators
	// Note: Godeps is a directory, not a file, so we check for it separately
	goFile := map[string]bool{"go.mod": true, "main.go": true, "Gopkg.toml": true}
	goDir := map[string]bool{"Godeps": true, "vendor": true} // Directories that indicate Go project
	cargoFile := map[string]bool{"Cargo.toml": true, "Cargo.lock": true}
	pipFile := map[string]bool{"requirements.txt": true, "setup.py": true, "Pipfile": true}

	data, err := global.MakeGitHubRequest[[]interface{}](url)
	if err != nil {
		return err
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

		// Check for Go files
		if goFile[name] {
			info.Generator = global.GoModGenerator
			return nil
		}

		// Check for Go directories (Godeps, vendor)
		if itemType == "dir" && goDir[name] {
			info.Generator = global.GoModGenerator
			return nil
		}

		if cargoFile[name] {
			info.Generator = global.CargoHomeGenerator
			return nil
		}

		if pipFile[name] {
			info.Generator = global.PipGenerator
			return nil
		}
	}

	if info.Generator == "" {
		return fmt.Errorf("❌  No recognized source generator files found; Supported: Go (go.mod), Rust (Cargo.toml), Python (requirements.txt, setup.py, Pipfile)")
	}

	return fmt.Errorf("❌ Unexpected error in determining source generator")
}

// FetchTagInfo fetches commit SHA for a specific tag
func FetchTagInfo(info *global.RepoInfo, tag string) error {
	// Try fetching tag as a release first
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/git/ref/tags/%s", info.Owner, info.Repo, tag)

	data, err := global.MakeGitHubRequest[map[string]interface{}](url)
	if err != nil {
		return err
	}

	// Extract SHA from object
	if object, ok := data["object"].(map[string]interface{}); ok {
		if sha, ok := object["sha"].(string); ok {
			info.LatestCommit = sha
			info.Version = tag
			return nil
		}
	}

	return fmt.Errorf("failed to extract commit SHA from tag")
}

// PrintRepoInfo displays repository information
func PrintRepoInfo(info *global.RepoInfo) {
	fmt.Println("📦 Repository Information")
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Printf("  Owner: %s\n", info.Owner)
	fmt.Printf("  Repo: %s\n", info.Repo)
	fmt.Printf("  Default Branch: %s\n", info.Branch)
	fmt.Printf("  Git URL: %s\n", info.GitURL)
	fmt.Printf("  Description: %s\n", info.Description)
	fmt.Printf("  Version: %s\n", info.Version)
	fmt.Printf("  License: %s\n", info.License)
	fmt.Printf("  Latest Commit: %s\n", info.LatestCommit)
	fmt.Printf("  Source Generator: %s\n", info.Generator)
	fmt.Println()
}
