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
	Owner        string
	Repo         string
	Branch       string
	GitURL       string
	Description  string
	License      string
	LatestCommit string
}

// FetchRepoInfo fetches repository metadata from GitHub API
func FetchRepoInfo(repoPath string) (*RepoInfo, error) {
	owner, repo, branch, err := parseRepoPath(repoPath)
	if err != nil {
		return nil, err
	}

	info := &RepoInfo{
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

	return info, nil
}

// parseRepoPath extracts owner and repo from various formats
// Supports: "owner/repo", "https://github.com/owner/repo", "github.com/owner/repo"
func parseRepoPath(path string) (owner, repo, branch string, err error) {
	// Remove trailing slash
	path = strings.TrimSuffix(path, "/")

	// Remove protocol if present
	path = strings.TrimPrefix(path, "https://")
	path = strings.TrimPrefix(path, "http://")
	path = strings.TrimPrefix(path, "github.com/")

	// Split by /
	parts := strings.Split(path, "/")
	n := len(parts)

	if n == 2 {
		owner, repo = parts[0], parts[1]
		return owner, repo, "main", nil
	} else if n == 4 && parts[2] == "tree" {
		owner, repo = parts[0], parts[1]
		return owner, repo, parts[3], nil
	} else {
		return "", "", "", fmt.Errorf("invalid repository path: %s (expected format: owner/repo/tree/branch)", path)
	}
}

// Acquire default metadata
func fetchRepoMetadata(info *RepoInfo) error {
	url := info.GitURL

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
	} else {
		info.Description = fmt.Sprintf("This is the %s project.", info.Repo)
	}

	if url, ok := data["html_url"].(string); ok && url != "" {
		info.GitURL = url
	}

	// TODO: Potentially unnecessary license extraction
	if license, ok := data["license"].(map[string]interface{}); ok {
		if spdxID, ok := license["spdx_id"].(string); ok && spdxID != "NOASSERTION" {
			info.License = spdxID
		}
	}

	return nil
}

// Acquire release metadata
func fetchReleaseMetadata(info *RepoInfo) error {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases", info.Owner, info.Repo)

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

	asset, ok := data["assets"].([]interface{})
	if !ok || len(asset) == 0 {
		return fmt.Errorf("no releases found for repository")
	}

	assetMap := asset[0].(map[string]interface{})
	digest, ok := assetMap["digest"].(string)
	if !ok {
		return fmt.Errorf("commit SHA not found in response")
	}

	parts := strings.Split(digest, ":")
	if len(parts) != 2 {
		return fmt.Errorf("invalid digest format")
	}

	_, sha := parts[0], parts[1]
	info.LatestCommit = sha

	return nil
}

// Request data from GitHub API
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

	return client.Do(req)
}

// PrintRepoInfo displays repository information
func PrintRepoInfo(info *RepoInfo) {
	fmt.Println("📦 Repository Information")
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Printf("  Owner: %s\n", info.Owner)
	fmt.Printf("  Repo: %s\n", info.Repo)
	fmt.Printf("  Default Branch: %s\n", info.Branch)
	fmt.Printf("  Git URL: %s\n", info.GitURL)
	fmt.Printf("  Description: %s\n", info.Description)
	fmt.Printf("  License: %s\n", info.License)
	fmt.Printf("  Latest Commit: %s\n", info.LatestCommit)
	fmt.Println()
}
