package repository

// ═══════════════════════════════════════════════════════════════════════════════
// github.go — GitHub API client and repository metadata.
//
//   Chunk 1 · URL PARSING              SplitGitHubComponent()
//   Chunk 2 · HTTP CLIENT              FetchRawContent(), FetchJSON(), FetchJSONArray(), WriteJSON()
//   Chunk 3 · REPOSITORY INFO          FetchRepoInfo(), fetchRepositorySegments()
//   Chunk 4 · METADATA                 fetchRepoMetadata(), fetchSourceGenerator()
//   Chunk 5 · TAGS                     FetchAllGithubTags()
// ═══════════════════════════════════════════════════════════════════════════════

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path"
	"strings"

	"dalec-mapping/domain/repository"
)

const githubAPIBase = "https://api.github.com"

// ─── Chunk 1 · URL PARSING ──────────────────────────────────────────────────

// SplitGitHubComponent splits a GitHub path (owner/repo or owner/repo/component/...)
// into its base ref and an optional component subdirectory.
// Also handles full https://github.com/owner/repo/... URLs.
//
// Example:
//
//	"owner/repo/test/npd"
//	→ baseRef   = "owner/repo"
//	  component = "test/npd"
func SplitGitHubComponent(repoRef string) (string, string) {
	repoRef = strings.TrimSuffix(repoRef, ".git")

	// Strip scheme+host prefix if present.
	prefix := ""
	for _, scheme := range []string{"https://github.com/", "http://github.com/"} {
		if strings.HasPrefix(repoRef, scheme) {
			prefix = scheme
			repoRef = strings.TrimPrefix(repoRef, scheme)
			break
		}
	}

	// Also handle owner/repo/tree/branch format — don't treat tree segments as component.
	parts := strings.Split(strings.Trim(repoRef, "/"), "/")
	if len(parts) >= 4 && parts[2] == "tree" {
		// owner/repo/tree/branch[/...] — no component
		return prefix + repoRef, ""
	}
	if len(parts) <= 2 {
		return prefix + repoRef, ""
	}
	base := prefix + parts[0] + "/" + parts[1]
	return base, strings.Join(parts[2:], "/")
}

// ─── Chunk 2 · HTTP CLIENT ──────────────────────────────────────────────────

// GithubReturnType controls how makeGitHubRequest decodes the response body.
type GithubReturnType int

const (
	ReturnJSON      GithubReturnType = iota // → map[string]interface{}
	ReturnJSONArray                         // → []map[string]interface{}
	ReturnRaw                               // → []byte
)

// makeGitHubRequest is the single internal HTTP entry-point.
// All public Fetch* functions delegate here.
func makeGitHubRequest(request repository.GithubRequest, returnType GithubReturnType) (interface{}, error) {
	var bodyReader io.Reader
	if request.Payload != nil && (request.Method == repository.POST || request.Method == repository.PUT || request.Method == repository.PATCH) {
		jsonBody, err := json.Marshal(request.Payload)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal request body: %w", err)
		}
		bodyReader = bytes.NewReader(jsonBody)
	}

	method := request.Method
	if method == "" {
		method = repository.GET
	}

	req, err := http.NewRequest(string(method), request.URL, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	if token := os.Getenv("GH_TOKEN"); token != "" {
		req.Header.Set("Authorization", "token "+token)
	}

	if returnType != ReturnRaw {
		req.Header.Set("Accept", "application/vnd.github.v3+json")
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("GitHub API returned status %d: %s", resp.StatusCode, string(body))
	}

	switch returnType {
	case ReturnRaw:
		return body, nil
	case ReturnJSONArray:
		var result []map[string]interface{}
		if err := json.Unmarshal(body, &result); err != nil {
			return nil, fmt.Errorf("failed to unmarshal JSON array: %w", err)
		}
		return result, nil
	default: // ReturnJSON
		var result map[string]interface{}
		if err := json.Unmarshal(body, &result); err != nil {
			return nil, fmt.Errorf("failed to unmarshal JSON: %w", err)
		}
		return result, nil
	}
}

// FetchRawContent fetches raw bytes from a URL (e.g. raw.githubusercontent.com).
func FetchRawContent(url string) ([]byte, error) {
	result, err := makeGitHubRequest(repository.GithubRequest{URL: url}, ReturnRaw)
	if err != nil {
		return nil, err
	}
	return result.([]byte), nil
}

// FetchJSON performs an authenticated GET to the GitHub API and returns a JSON object.
// path is relative to api.github.com (e.g. "repos/owner/repo/contents/file").
func FetchJSON(path string) (map[string]interface{}, error) {
	result, err := makeGitHubRequest(repository.GithubRequest{URL: githubAPIBase + "/" + path}, ReturnJSON)
	if err != nil {
		return nil, err
	}
	return result.(map[string]interface{}), nil
}

// FetchJSONArray performs an authenticated GET to the GitHub API and returns a JSON array.
// path is relative to api.github.com.
func FetchJSONArray(path string) ([]map[string]interface{}, error) {
	result, err := makeGitHubRequest(repository.GithubRequest{URL: githubAPIBase + "/" + path}, ReturnJSONArray)
	if err != nil {
		return nil, err
	}
	return result.([]map[string]interface{}), nil
}

// WriteJSON performs a write (PUT/POST) to the GitHub API and returns a JSON object.
// path is relative to api.github.com.
// WriteJSON performs a write (PUT/POST) to the GitHub API.
// Returns the response as a JSON object when possible; returns (nil, nil) for
// non-object responses (e.g. the labels API returns an array).
func WriteJSON(path string, method repository.CRUDRequest, payload interface{}) (map[string]interface{}, error) {
	raw, err := makeGitHubRequest(repository.GithubRequest{
		URL:     githubAPIBase + "/" + path,
		Method:  method,
		Payload: payload,
	}, ReturnRaw)
	if err != nil {
		return nil, err
	}
	var m map[string]interface{}
	if json.Unmarshal(raw.([]byte), &m) != nil {
		return nil, nil
	}
	return m, nil
}

// ─── Chunk 3 · REPOSITORY INFO ──────────────────────────────────────────────

// FetchRepoInfo fetches repository metadata from the GitHub API.
func FetchRepoInfo(repoPath string) (*repository.RepoInfo, error) {
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

	if err := fetchRepoMetadata(info); err != nil {
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

// ─── Chunk 4 · METADATA ────────────────────────────────────────────────────

// fetchRepoMetadata acquires default branch, description, URL, and license.
func fetchRepoMetadata(info *repository.RepoInfo) error {
	data, err := FetchJSON(fmt.Sprintf("repos/%s/%s", info.Owner, info.Repo))
	if err != nil {
		return err
	}

	if info.Branch == "" {
		if defaultBranch, ok := data["default_branch"].(string); ok {
			info.Branch = defaultBranch
		} else {
			info.Branch = "main"
		}
	}

	if desc, ok := data["description"].(string); ok {
		info.Description = desc
	} else {
		info.Description = fmt.Sprintf("This is the %s project.", info.Repo)
	}

	if url, ok := data["html_url"].(string); ok && url != "" {
		info.GitURL = url
	}

	info.License = "proprietary"
	if license, ok := data["license"].(map[string]interface{}); ok {
		if spdxID, ok := license["spdx_id"].(string); ok && spdxID != "NOASSERTION" {
			info.License = spdxID
		}
	}

	return nil
}

// fetchSourceGenerator detects the project's build system by scanning the repo tree.
func fetchSourceGenerator(info *repository.RepoInfo) error {
	fileGenerators := map[string]repository.SourceGenerator{
		"go.mod":           repository.GoModGenerator,
		"main.go":          repository.GoModGenerator,
		"Gopkg.toml":       repository.GoModGenerator,
		"Cargo.toml":       repository.CargoHomeGenerator,
		"Cargo.lock":       repository.CargoHomeGenerator,
		"requirements.txt": repository.PipGenerator,
		"setup.py":         repository.PipGenerator,
		"Pipfile":          repository.PipGenerator,
	}
	dirGenerators := map[string]repository.SourceGenerator{
		"Godeps": repository.GoModGenerator,
		"vendor": repository.GoModGenerator,
	}

	data, err := FetchJSON(fmt.Sprintf("repos/%s/%s/git/trees/%s?recursive=1", info.Owner, info.Repo, info.Branch))
	if err != nil {
		return fmt.Errorf("failed to fetch repository tree: %w", err)
	}

	treeItems, ok := data["tree"].([]interface{})
	if !ok {
		return fmt.Errorf("unexpected tree response format")
	}

	scanItems := func(prefix string) (repository.SourceGenerator, bool) {
		for _, item := range treeItems {
			itemMap, ok := item.(map[string]interface{})
			if !ok {
				continue
			}
			p, _ := itemMap["path"].(string)
			itemType, _ := itemMap["type"].(string)

			if prefix != "" && !strings.HasPrefix(p, prefix+"/") {
				continue
			}

			base := p[strings.LastIndex(p, "/")+1:]

			if gen, ok := fileGenerators[base]; ok && itemType == "blob" {
				return gen, true
			}
			if gen, ok := dirGenerators[base]; ok && itemType == "tree" {
				return gen, true
			}
		}
		return "", false
	}

	if info.ComponentPath != "" {
		log.Printf("Searching for source generator under component path '%s'...\n", info.ComponentPath)

		if gen, ok := scanItems(info.ComponentPath); ok {
			info.Generator = gen
			return nil
		}

		subdirBase := info.ComponentPath[strings.LastIndex(info.ComponentPath, "/")+1:]
		for _, item := range treeItems {
			itemMap, ok := item.(map[string]interface{})
			if !ok {
				continue
			}
			p, _ := itemMap["path"].(string)
			itemType, _ := itemMap["type"].(string)
			if itemType != "tree" {
				continue
			}
			base := p[strings.LastIndex(p, "/")+1:]
			if base != subdirBase {
				continue
			}
			if gen, ok := scanItems(p); ok {
				info.Generator = gen
				return nil
			}
		}
	}

	if gen, ok := scanItems(""); ok {
		info.Generator = gen
		return nil
	}

	return fmt.Errorf("❌  No recognized source generator files found; Supported: Go (go.mod), Rust (Cargo.toml), Python (requirements.txt, setup.py, Pipfile)")
}

// ─── Chunk 5 · TAGS ────────────────────────────────────────────────────────

// FetchAllGithubTags fetches all git tags for a repository using the high-level
// tags API, which returns the dereferenced commit SHA directly without additional
// per-tag round-trips. Returns a map of tagName → commitSHA.
// repoURL is a GitHub path like "owner/repo" or full URL.
// Fetches all pages (100 tags per page).
func FetchAllGithubTags(repoURL string) (map[string]string, error) {
	owner, repo, _ := fetchRepositorySegments(repoURL)
	allTags := map[string]string{}
	page := 1
	for {
		pageData, err := FetchJSONArray(fmt.Sprintf("repos/%s/%s/tags?per_page=100&page=%d", owner, repo, page))
		if err != nil {
			return nil, fmt.Errorf("failed to fetch tags for %s/%s (page %d): %w", owner, repo, page, err)
		}
		if len(pageData) == 0 {
			break
		}

		for _, item := range pageData {
			name, ok := item["name"].(string)
			if !ok {
				continue
			}
			commit, ok := item["commit"].(map[string]interface{})
			if !ok {
				continue
			}
			sha, _ := commit["sha"].(string)
			allTags[name] = sha
		}

		if len(pageData) < 100 {
			break
		}
		page++
	}
	return allTags, nil
}

