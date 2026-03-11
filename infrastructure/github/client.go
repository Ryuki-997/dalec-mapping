package github

import (
	"fmt"
	"log"
	"regexp"
	"strings"

	"dalec-mapping/domain/repository"
)

// semverInTag extracts a clean vX.Y.Z from tags that may have suffixes (e.g. v1.2.3-main-date-hash).
var semverInTag = regexp.MustCompile(`v(\d+)\.(\d+)\.(\d+)`)

// FetchRepoInfo fetches repository metadata from GitHub API
func FetchRepoInfo(repoPath, subdir, tag string) (*repository.RepoInfo, error) {
	owner, repo, branch := ExtractRepositorySegments(repoPath)

	fmt.Printf("Parsed - Owner: %s, Repo: %s, Branch: %s, Subdir: %s\n", owner, repo, branch, subdir)

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
	// Map each generator indicator filename to its SourceGenerator type.
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
	// Directory basenames that also indicate a Go project.
	dirGenerators := map[string]repository.SourceGenerator{
		"Godeps": repository.GoModGenerator,
		"vendor": repository.GoModGenerator,
	}

	// Fetch the full recursive tree in one call.
	treeURL := fmt.Sprintf("https://api.github.com/repos/%s/%s/git/trees/%s?recursive=1", info.Owner, info.Repo, info.Branch)
	data, err := MakeGitHubRequest[map[string]interface{}](repository.GithubRequest{URL: treeURL})
	if err != nil {
		return fmt.Errorf("failed to fetch repository tree: %w", err)
	}

	treeItems, ok := data["tree"].([]interface{})
	if !ok {
		return fmt.Errorf("unexpected tree response format")
	}

	// scanItems walks treeItems and returns the first matching generator.
	// When prefix is non-empty only paths under that prefix are considered.
	scanItems := func(prefix string) (repository.SourceGenerator, bool) {
		for _, item := range treeItems {
			itemMap, ok := item.(map[string]interface{})
			if !ok {
				continue
			}
			p, _ := itemMap["path"].(string)
			itemType, _ := itemMap["type"].(string)

			// Apply optional prefix filter.
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

	// When a subdir hint is provided, search under all paths that contain that
	// directory name — the monorepo may nest it deeper than the subdir alone
	// (e.g. subdir="otel-allocator" but go.mod lives at otelcollector/otel-allocator/go.mod).
	if info.Subdir != "" {
		log.Printf("Searching for source generator under subdir hint '%s'...\n", info.Subdir)

		// 1. Exact prefix match first (fast path).
		if gen, ok := scanItems(info.Subdir); ok {
			info.Generator = gen
			return nil
		}

		// 2. Any path component matches the subdir name (handles nested monorepos).
		subdirBase := info.Subdir[strings.LastIndex(info.Subdir, "/")+1:]
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
			// Found a directory with the matching name — search inside it.
			if gen, ok := scanItems(p); ok {
				info.Generator = gen
				return nil
			}
		}
	}

	// Fall back: search the entire tree (no prefix).
	if gen, ok := scanItems(""); ok {
		info.Generator = gen
		return nil
	}

	return fmt.Errorf("❌  No recognized source generator files found; Supported: Go (go.mod), Rust (Cargo.toml), Python (requirements.txt, setup.py, Pipfile)")
}

// fetchTagInfo fetches the commit SHA for a tag and populates info.LatestCommit and info.Version.
// tag is the stripped semver (e.g. "v0.4.0"). It first tries a direct git ref lookup;
// if that 404s (e.g. the actual ref is "azure-ipam/v0.4.0"), it calls FetchAllTags once
// to find the matching full ref and retries. Version is set to the plain semver (e.g. "0.4.0").
func fetchTagInfo(info *repository.RepoInfo, tag string) error {
	if tag == "" {
		return fmt.Errorf("Tag must be specified")
	}

	fullTag := tag
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/git/ref/tags/%s", info.Owner, info.Repo, fullTag)
	data, err := MakeGitHubRequest[map[string]interface{}](repository.GithubRequest{URL: url})
	if err != nil {
		// Direct lookup failed — search all git tags for one whose semver portion matches.
		_, allGitTags, fetchErr := FetchAllTags(info.Owner, info.Repo)
		if fetchErr != nil {
			return fmt.Errorf("tag %q not found and could not fetch tag list: %w", tag, fetchErr)
		}
		for _, t := range allGitTags {
			if semverInTag.FindString(t) == tag {
				fullTag = t
				break
			}
		}
		if fullTag == tag {
			return fmt.Errorf("tag %q not found in repository", tag)
		}
		url = fmt.Sprintf("https://api.github.com/repos/%s/%s/git/ref/tags/%s", info.Owner, info.Repo, fullTag)
		data, err = MakeGitHubRequest[map[string]interface{}](repository.GithubRequest{URL: url})
		if err != nil {
			return err
		}
	}

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