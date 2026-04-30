package repository

// ═══════════════════════════════════════════════════════════════════════════════
// repository.go — GitHub API client and repository metadata fetching.
//
//   Chunk 1 · HTTP CLIENT              FetchRawContent(), FetchJSON(), FetchJSONArray()
//     Public Fetch wrappers over private makeGitHubRequest().
//
//   Chunk 2 · REPOSITORY INFO          FetchRepoInfo(), FetchRepositorySegments()
//     Entry-point for populating RepoInfo; URL/path parsing.
//
//   Chunk 3 · METADATA                 fetchRepoMetadata(), fetchReleaseMetadata(),
//                                       fetchSourceGenerator()
//     Branch, license, version, commit SHA, and source-generator detection.
//
//   Chunk 4 · TAGS                     fetchTagInfo(), FetchTagCommit(), FetchAllTags()
//     Tag-to-commit resolution and paginated tag listing.
// ═══════════════════════════════════════════════════════════════════════════════

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"

	"dalec-mapping/domain/repository"
)

const githubAPIBase = "https://api.github.com"

// ─── Chunk 1 · HTTP CLIENT ──────────────────────────────────────────────────

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
	if request.Payload != nil && (request.Method == repository.POST || request.Method == repository.PUT) {
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

// ─── Chunk 2 · REPOSITORY INFO ──────────────────────────────────────────────

// FetchRepoInfo fetches repository metadata from the GitHub API.
func FetchRepoInfo(repoPath, tag string) (*repository.RepoInfo, error) {
	baseRef, componentPath := SplitComponent(repoPath)
	owner, repo, branch := FetchRepositorySegments(baseRef)

	log.Printf("Parsed - Owner: %s, Repo: %s, Branch: %s, ComponentPath: %s\n", owner, repo, branch, componentPath)

	info := &repository.RepoInfo{
		Owner:         owner,
		Repo:          repo,
		Branch:        branch,
		ComponentPath: componentPath,
		ComponentName: ComponentName(componentPath),
		GitURL:        fmt.Sprintf("https://github.com/%s/%s", owner, repo),
	}

	if err := fetchRepoMetadata(info); err != nil {
		return nil, fmt.Errorf("failed to fetch repo metadata: %w", err)
	}
	if err := fetchSourceGenerator(info); err != nil {
		return nil, fmt.Errorf("failed to fetch source generator: %w", err)
	}
	if err := fetchTagInfo(info, tag); err != nil {
		return nil, fmt.Errorf("failed to fetch tag when tag is provided: %w", err)
	}
	return info, nil
}

// FetchRepositorySegments extracts the repository segments from a GitHub URL or path.
func FetchRepositorySegments(repo string) (owner, name, branch string) {
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

// ─── Chunk 3 · METADATA ────────────────────────────────────────────────────

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

	info.License = "foo-license"
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

// ─── Chunk 4 · TAGS ────────────────────────────────────────────────────────

// fetchTagInfo resolves `tag` to a commit SHA and populates info.LatestCommit and info.Version.
// Looks up the tag via the commits API to resolve to the actual commit SHA.
// If a direct lookup fails, it searches all git tags for a matching semver.
func fetchTagInfo(info *repository.RepoInfo, tag string) error {
	if tag == "" {
		return fmt.Errorf("Tag must be specified")
	}

	fullTag := tag

	// Try resolving the tag directly via commits API
	commitData, err := FetchJSON(fmt.Sprintf("repos/%s/%s/commits/%s", info.Owner, info.Repo, fullTag))
	if err != nil {
		// Tag not found directly — search all tags for a matching semver
		allTags, fetchErr := FetchAllGithubTags(info.Owner, info.Repo)
		if fetchErr != nil {
			return fmt.Errorf("tag %q not found and could not fetch tag list: %w", tag, fetchErr)
		}
		for _, t := range allTags {
			if semverTagRe.FindString(t.Name) == tag {
				fullTag = t.Name
				break
			}
		}
		if fullTag == tag {
			return fmt.Errorf("tag %q not found in repository", tag)
		}
		// Resolve the matched full tag
		commitData, err = FetchJSON(fmt.Sprintf("repos/%s/%s/commits/%s", info.Owner, info.Repo, fullTag))
		if err != nil {
			return fmt.Errorf("failed to resolve commit for tag %q: %w", fullTag, err)
		}
	}

	// Extract version from the tag
	if m := semverTagRe.FindString(tag); m != "" {
		info.Version = strings.TrimPrefix(m, "v")
	} else {
		info.Version = strings.TrimPrefix(tag, "v")
	}

	if sha, ok := commitData["sha"].(string); ok {
		info.LatestCommit = sha
		return nil
	}

	return fmt.Errorf("failed to extract commit SHA from tag %q", fullTag)
}

// FetchTagCommit resolves a git tag to its release commit SHA for the given owner/repo.
// Uses the commits API to dereference annotated tags to the actual commit.
func FetchTagCommit(owner, repo, tagRef string) (string, error) {
	data, err := FetchJSON(fmt.Sprintf("repos/%s/%s/commits/%s", owner, repo, tagRef))
	if err != nil {
		return "", fmt.Errorf("failed to resolve commit for tag %q in %s/%s: %w", tagRef, owner, repo, err)
	}
	if sha, ok := data["sha"].(string); ok {
		return sha, nil
	}
	return "", fmt.Errorf("failed to extract commit SHA from tag %q", tagRef)
}

// FetchAllGithubTags fetches all git tags for a repository.
// Each TagInfo carries the tag name and its commit SHA.
func FetchAllGithubTags(owner, repo string) ([]TagInfo, error) {
	tagData, err := FetchJSONArray(fmt.Sprintf("repos/%s/%s/git/refs/tags", owner, repo))
	if err != nil {
		return nil, fmt.Errorf("failed to fetch tags for %s/%s: %w", owner, repo, err)
	}

	var tags []TagInfo
	for _, item := range tagData {
		ref, ok := item["ref"].(string)
		if !ok {
			continue
		}
		name := strings.TrimPrefix(ref, "refs/tags/")

		var sha string
		if obj, ok := item["object"].(map[string]interface{}); ok {
			sha, _ = obj["sha"].(string)
		}

		tags = append(tags, TagInfo{Name: name, Commit: sha})
	}
	return tags, nil
}

// FetchRemoteSpecCommit fetches a spec file from the given repo/branch and parses
// the args.COMMIT value from the YAML. Returns the commit SHA stored in the spec.
func FetchRemoteSpecCommit(specFilePath, owner, repo, branch string) (string, error) {
	contentsPath := fmt.Sprintf("repos/%s/%s/contents/%s?ref=%s", owner, repo, specFilePath, branch)

	data, err := FetchJSON(contentsPath)
	if err != nil {
		return "", fmt.Errorf("failed to fetch spec file %s: %w", specFilePath, err)
	}

	contentStr, ok := data["content"].(string)
	if !ok {
		return "", fmt.Errorf("unexpected response: missing content field for %s", specFilePath)
	}

	// Decode base64 content
	cleaned := strings.ReplaceAll(contentStr, "\n", "")
	decoded, err := base64.StdEncoding.DecodeString(cleaned)
	if err != nil {
		return "", fmt.Errorf("failed to decode content for %s: %w", specFilePath, err)
	}

	// Parse COMMIT from args section using simple line scanning
	commit := parseArgFromYAML(string(decoded), "COMMIT")
	if commit == "" {
		return "", fmt.Errorf("args.COMMIT not found in %s", specFilePath)
	}
	return commit, nil
}

// parseArgFromYAML extracts a named arg value from a YAML spec string.
// Scans for lines like "  COMMIT: abc123" within the args block.
func parseArgFromYAML(content, argName string) string {
	inArgs := false
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "args:" {
			inArgs = true
			continue
		}
		if inArgs {
			// End of args block (next top-level key)
			if len(line) > 0 && line[0] != ' ' && line[0] != '\t' {
				break
			}
			if strings.HasPrefix(trimmed, argName+":") {
				value := strings.TrimPrefix(trimmed, argName+":")
				return strings.TrimSpace(value)
			}
		}
	}
	return ""
}
