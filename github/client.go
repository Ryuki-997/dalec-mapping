package github

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type SourceGenerator string

const (
	GoModGenerator     SourceGenerator = "gomod"
	CargoHomeGenerator SourceGenerator = "cargohome"
	PipGenerator       SourceGenerator = "pip"
)

// RepoInfo contains metadata about a GitHub repository
type RepoInfo struct {
	Owner        string
	Repo         string
	Branch       string
	GitURL       string
	Description  string
	Version      string
	License      string
	LatestCommit string
	Generator    SourceGenerator
}

// FetchRepoInfo fetches repository metadata from GitHub API
func FetchRepoInfo(repoPath string) (*RepoInfo, error) {
	owner, repo, branch, err := parseRepoPath(repoPath)
	if err != nil {
		return nil, err
	}

	fmt.Printf("Parsed - Owner: %s, Repo: %s, Branch: %s\n", owner, repo, branch)

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

	// Fetch source generator
	if err := fetchSourceGenerator(info); err != nil {
		return nil, fmt.Errorf("failed to fetch source generator: %w", err)
	}

	return info, nil
}

// parseRepoPath extracts owner and repo from various formats
// Supports:
// - "owner/repo"
// - "https://github.com/owner/repo"
// - "github.com/owner/repo/tree/branch"
// - "github.com/owner/repo/tree/branch/subdirectory/path"
func parseRepoPath(path string) (owner, repo, branch string, err error) {
	// Remove trailing slash
	path = strings.TrimSuffix(path, "/")

	// Remove protocol if present
	path = strings.TrimPrefix(path, "https://")
	path = strings.TrimPrefix(path, "http://")
	path = strings.TrimPrefix(path, "github.com/")

	// Split by /
	parts := strings.SplitN(path, "/", 4)
	n := len(parts)

	if n < 2 {
		return "", "", "", fmt.Errorf("invalid repository path: %s (expected format: owner/repo)", path)
	}

	owner, repo = parts[0], parts[1]

	// Check if there's a branch specification
	if n >= 4 && parts[2] == "tree" {
		branch = parts[3]
	} else if n == 2 {
		branch = "main"
	} else {
		return "", "", "", fmt.Errorf("invalid repository path: %s (expected format: owner/repo or owner/repo/tree/branch)", path)
	}

	return owner, repo, branch, nil
}

// Acquire default metadata
func fetchRepoMetadata(info *RepoInfo) error {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s", info.Owner, info.Repo)

	data, err := makeGitHubMapRequest(url)
	if err != nil {
		return err
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
func fetchReleaseMetadata(info *RepoInfo) error {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/latest", info.Owner, info.Repo)

	data, err := makeGitHubMapRequest(url)
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
	data, err = makeGitHubMapRequest(url)
	if err != nil {
		return err
	}

	if sha, ok := data["sha"].(string); ok {
		info.LatestCommit = sha
	}

	return nil
}

func fetchSourceGenerator(info *RepoInfo) error {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/contents", info.Owner, info.Repo)

	// Verified files susceptible for upstream generators
	goFile := map[string]bool{"go.mod": true, "main.go": true}
	cargoFile := map[string]bool{"Cargo.toml": true, "Cargo.lock": true}
	pipFile := map[string]bool{"requirements.txt": true, "setup.py": true, "Pipfile": true}

	data, err := makeGitHubArrayRequest(url)
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

		if goFile[name] {
			info.Generator = GoModGenerator
			return nil
		} else if cargoFile[name] {
			info.Generator = CargoHomeGenerator
			return nil
		} else if pipFile[name] {
			info.Generator = PipGenerator
			return nil
		}
	}

	if info.Generator == "" {
		return fmt.Errorf("❌  No recognized source generator files found; Supported: Go (go.mod), Rust (Cargo.toml), Python (requirements.txt, setup.py, Pipfile)")
	}

	return fmt.Errorf("❌ Unexpected error in determining source generator")
}

// FetchTagInfo fetches commit SHA for a specific tag
func FetchTagInfo(info *RepoInfo, tag string) error {
	// Try fetching tag as a release first
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/git/ref/tags/%s", info.Owner, info.Repo, tag)

	data, err := makeGitHubMapRequest(url)
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

func makeGitHubMapRequest(url string) (map[string]interface{}, error) {
	client := &http.Client{
		Timeout: 10 * time.Second,
	}

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Add headers for GitHub API
	req.Header.Set("Accept", "application/vnd.github.v3+json")

	resp, err := client.Do(req)

	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API error fetching tag: %s - %s", resp.Status, string(body))
	}

	var data map[string]interface{}
	if err := json.Unmarshal(body, &data); err != nil {
		return nil, fmt.Errorf("failed to parse JSON: %w", err)
	}

	return data, nil
}

func makeGitHubArrayRequest(url string) ([]interface{}, error) {
	client := &http.Client{
		Timeout: 10 * time.Second,
	}

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Accept", "application/vnd.github.v3+json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API error: %s - %s", resp.Status, string(body))
	}

	var data []interface{}
	if err := json.Unmarshal(body, &data); err != nil {
		return nil, fmt.Errorf("failed to parse JSON: %w", err)
	}

	return data, nil
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
	fmt.Printf("  Version: %s\n", info.Version)
	fmt.Printf("  License: %s\n", info.License)
	fmt.Printf("  Latest Commit: %s\n", info.LatestCommit)
	fmt.Printf("  Source Generator: %s\n", info.Generator)
	fmt.Println()
}
