package github

import (
	"fmt"
	"regexp"
	"strings"

	"dalec-mapping/domain/repository"
)

// semverInTag extracts a clean vX.Y.Z from tags that may have suffixes (e.g. v1.2.3-main-date-hash).
var semverInTag = regexp.MustCompile(`v(\d+)\.(\d+)\.(\d+)`)

// FetchRepoInfo fetches repository metadata from GitHub API
func FetchRepoInfo(repoPath, tag string) (*repository.RepoInfo, error) {
	owner, repo, branch, subdir := ExtractRepositorySegments(repoPath)

	fmt.Printf("Parsed - Owner: %s, Repo: %s, Branch: %s\n", owner, repo, branch)

	info := &repository.RepoInfo{
		Owner:  owner,
		Repo:   repo,
		Branch: branch,
		Subdir: subdir,
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

	// Fetch specified tag
	if err := fetchTagInfo(info, tag); err != nil {
		return nil, fmt.Errorf("failed to fetch tag when tag is provided: %w", err)
	}

	return info, nil
}

// Acquire default metadata
func fetchRepoMetadata(info *repository.RepoInfo) error {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s", info.Owner, info.Repo)

	data, err := MakeGitHubRequest[map[string]interface{}](repository.GithubRequest{URL: url})
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
func fetchReleaseMetadata(info *repository.RepoInfo) error {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/latest", info.Owner, info.Repo)

	data, err := MakeGitHubRequest[map[string]interface{}](repository.GithubRequest{URL: url})
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
	if m := semverInTag.FindString(tag); m != "" {
		info.Version = m
	} else {
		info.Version = tag
	}

	// Fetch the commit SHA for this release tag
	url = fmt.Sprintf("https://api.github.com/repos/%s/%s/commits/%s", info.Owner, info.Repo, tag)
	data, err = MakeGitHubRequest[map[string]interface{}](repository.GithubRequest{URL: url})
	if err != nil {
		return err
	}

	if sha, ok := data["sha"].(string); ok {
		info.LatestCommit = sha
	}

	return nil
}

func fetchSourceGenerator(info *repository.RepoInfo) error {
	// Map each generator's indicator filenames to its SourceGenerator type.
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
	// Directory names that also indicate a Go project.
	dirGenerators := map[string]repository.SourceGenerator{
		"Godeps": repository.GoModGenerator,
		"vendor": repository.GoModGenerator,
	}

	if info.Subdir != "" {
		// Subdirectory provided — fetch its direct contents via the Contents API.
		// This is non-recursive: we only look at the immediate children of subdir.
		contentsURL := fmt.Sprintf("https://api.github.com/repos/%s/%s/contents/%s?ref=%s",
			info.Owner, info.Repo, info.Subdir, info.Branch)

		items, err := MakeGitHubRequest[[]map[string]interface{}](repository.GithubRequest{URL: contentsURL})
		if err != nil {
			return fmt.Errorf("failed to fetch contents of %s: %w", info.Subdir, err)
		}

		for _, item := range items {
			name, _ := item["name"].(string)
			itemType, _ := item["type"].(string) // "file" or "dir"

			if gen, ok := fileGenerators[name]; ok && itemType == "file" {
				info.Generator = gen
				return nil
			}
			if gen, ok := dirGenerators[name]; ok && itemType == "dir" {
				info.Generator = gen
				return nil
			}
		}

		if info.Generator == "" {
			return fmt.Errorf("❌  No recognized source generator files found in %s; Supported: Go (go.mod), Rust (Cargo.toml), Python (requirements.txt, setup.py, Pipfile)", info.Subdir)
		}
		return nil
	}

	// No subdirectory — fetch the top-level tree (non-recursive) to find generator indicators.
	treeURL := fmt.Sprintf("https://api.github.com/repos/%s/%s/git/trees/%s", info.Owner, info.Repo, info.Branch)

	data, err := MakeGitHubRequest[map[string]interface{}](repository.GithubRequest{URL: treeURL})
	if err != nil {
		return fmt.Errorf("failed to fetch repository tree: %w", err)
	}

	treeItems, ok := data["tree"].([]interface{})
	if !ok {
		return fmt.Errorf("unexpected tree response format")
	}

	for _, item := range treeItems {
		itemMap, ok := item.(map[string]interface{})
		if !ok {
			continue
		}

		path, ok := itemMap["path"].(string)
		if !ok {
			continue
		}

		itemType, _ := itemMap["type"].(string)

		if gen, ok := fileGenerators[path]; ok && itemType == "blob" {
			info.Generator = gen
			break
		}

		if gen, ok := dirGenerators[path]; ok && itemType == "tree" {
			info.Generator = gen
			break
		}
	}

	if info.Generator == "" {
		return fmt.Errorf("❌  No recognized source generator files found; Supported: Go (go.mod), Rust (Cargo.toml), Python (requirements.txt, setup.py, Pipfile)")
	}

	return nil
}

// fetchTagInfo fetches commit SHA for a specific tag
func fetchTagInfo(info *repository.RepoInfo, tag string) error {
	if tag == "" {
		return fmt.Errorf("Tag must be specified")
	}

	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/git/ref/tags/%s", info.Owner, info.Repo, tag)

	data, err := MakeGitHubRequest[map[string]interface{}](repository.GithubRequest{URL: url})
	if err != nil {
		return err
	}

	// Extract SHA from object
	if object, ok := data["object"].(map[string]interface{}); ok {
		if sha, ok := object["sha"].(string); ok {
			info.LatestCommit = sha
			if m := semverInTag.FindString(tag); m != "" {
				info.Version = strings.TrimPrefix(m, "v")
			} else {
				info.Version = strings.TrimPrefix(tag, "v")
			}
			return nil
		}
	}

	return fmt.Errorf("failed to extract commit SHA from tag")
}

// FetchAllTags fetches all tags for a repository, returning both the
// release-filtered tags and the full set of git tags.
// releaseTags only includes tags that have an associated GitHub release.
// allGitTags includes every tag in the repository.
func FetchAllTags(owner, repo string) (releaseTags []string, allGitTags []string, err error) {
	// Build a set of tags that have real GitHub releases
	releaseSet := make(map[string]bool)
	page := 1
	for {
		url := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases?per_page=100&page=%d", owner, repo, page)
		data, err := MakeGitHubRequest[[]map[string]interface{}](repository.GithubRequest{URL: url})
		if err != nil || len(data) == 0 {
			break
		}
		for _, release := range data {
			if tag, ok := release["tag_name"].(string); ok {
				releaseSet[tag] = true
			}
		}
		if len(data) < 100 {
			break
		}
		page++
	}

	// Fetch all git tags — the refs/tags endpoint returns all tags in a single response
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/git/refs/tags", owner, repo)
	tagData, fetchErr := MakeGitHubRequest[[]map[string]interface{}](repository.GithubRequest{URL: url})
	if fetchErr != nil {
		return nil, nil, fmt.Errorf("failed to fetch tags for %s/%s: %w", owner, repo, fetchErr)
	}
	for _, item := range tagData {
		ref, ok := item["ref"].(string)
		if !ok {
			continue
		}
		ref = strings.TrimPrefix(ref, "refs/tags/")
		allGitTags = append(allGitTags, ref)

		// If we have release data, only include tags with a real release
		if len(releaseSet) > 0 && !releaseSet[ref] {
			continue
		}
		releaseTags = append(releaseTags, ref)
	}
	return releaseTags, allGitTags, nil
}