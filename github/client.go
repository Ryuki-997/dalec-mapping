package github

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// RepoInfo contains metadata about a GitHub repository
type RepoInfo struct {
	Owner         string
	Repo          string
	FullName      string
	Description   string
	Website       string // Homepage URL
	GitURL        string // Clone URL
	License       string
	LatestCommit  string
	DefaultBranch string
}

// FetchRepoInfo fetches repository metadata from GitHub API
func FetchRepoInfo(repoPath string) (*RepoInfo, error) {
	owner, repo, err := parseRepoPath(repoPath)
	if err != nil {
		return nil, err
	}

	info := &RepoInfo{
		Owner:    owner,
		Repo:     repo,
		FullName: fmt.Sprintf("%s/%s", owner, repo),
		Website:  fmt.Sprintf("https://github.com/%s/%s", owner, repo),
		GitURL:   fmt.Sprintf("https://github.com/%s/%s", owner, repo),
	}

	// Fetch repository metadata
	if err := fetchRepoMetadata(info); err != nil {
		return nil, fmt.Errorf("failed to fetch repo metadata: %w", err)
	}

	// Fetch latest commit
	if err := fetchLatestCommit(info); err != nil {
		return nil, fmt.Errorf("failed to fetch latest commit: %w", err)
	}

	return info, nil
}

// parseRepoPath extracts owner and repo from various formats
// Supports: "owner/repo", "https://github.com/owner/repo", "github.com/owner/repo"
func parseRepoPath(path string) (owner, repo string, err error) {
	// Remove trailing slash
	path = strings.TrimSuffix(path, "/")

	// Remove protocol if present
	path = strings.TrimPrefix(path, "https://")
	path = strings.TrimPrefix(path, "http://")
	path = strings.TrimPrefix(path, "github.com/")

	// Split by /
	parts := strings.Split(path, "/")
	if len(parts) < 2 {
		return "", "", fmt.Errorf("invalid repository path: %s (expected format: owner/repo)", path)
	}

	return parts[0], parts[1], nil
}

// fetchRepoMetadata fetches repository information from GitHub API
func fetchRepoMetadata(info *RepoInfo) error {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s", info.Owner, info.Repo)

	resp, err := makeGitHubRequest(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GitHub API error: %s - %s", resp.Status, string(body))
	}

	var data map[string]interface{}
	if err := json.Unmarshal(body, &data); err != nil {
		return fmt.Errorf("failed to parse JSON: %w", err)
	}

	// Extract metadata
	if desc, ok := data["description"].(string); ok {
		info.Description = desc
	}

	if homepage, ok := data["homepage"].(string); ok && homepage != "" {
		info.Website = homepage
	}

	if branch, ok := data["default_branch"].(string); ok {
		info.DefaultBranch = branch
	} else {
		info.DefaultBranch = "main"
	}

	if license, ok := data["license"].(map[string]interface{}); ok {
		if spdxID, ok := license["spdx_id"].(string); ok && spdxID != "NOASSERTION" {
			info.License = spdxID
		}
	}

	return nil
}

// fetchLatestCommit fetches the latest commit SHA from the default branch
func fetchLatestCommit(info *RepoInfo) error {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/commits/%s",
		info.Owner, info.Repo, info.DefaultBranch)

	resp, err := makeGitHubRequest(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GitHub API error: %s - %s", resp.Status, string(body))
	}

	var data map[string]interface{}
	if err := json.Unmarshal(body, &data); err != nil {
		return fmt.Errorf("failed to parse JSON: %w", err)
	}

	if sha, ok := data["sha"].(string); ok {
		info.LatestCommit = sha
	} else {
		return fmt.Errorf("commit SHA not found in response")
	}

	return nil
}

// makeGitHubRequest creates an HTTP request with proper headers
func makeGitHubRequest(url string) (*http.Response, error) {
	client := &http.Client{
		Timeout: 10 * time.Second,
	}

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Add headers for GitHub API
	req.Header.Set("Accept", "application/vnd.github.v3+json")
	req.Header.Set("User-Agent", "dalec-mapping-cli")

	return client.Do(req)
}

// PrintRepoInfo displays repository information
func PrintRepoInfo(info *RepoInfo) {
	fmt.Println("📦 Repository Information")
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Printf("  Repository: %s\n", info.FullName)
	fmt.Printf("  Website: %s\n", info.Website)
	fmt.Printf("  Git URL: %s\n", info.GitURL)

	if info.Description != "" {
		fmt.Printf("  Description: %s\n", info.Description)
	}

	if info.License != "" {
		fmt.Printf("  License: %s\n", info.License)
	}

	fmt.Printf("  Default Branch: %s\n", info.DefaultBranch)
	fmt.Printf("  Latest Commit: %s\n", info.LatestCommit)
	fmt.Println()
}
