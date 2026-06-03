package github

// ─── Chunk 5 · TAGS ────────────────────────────────────────────────────────

import "fmt"

// FetchAllGithubTags fetches all git tags for a repository using the high-level
// tags API, which returns the dereferenced commit SHA directly without additional
// per-tag round-trips. Returns a map of tagName → commitSHA.
// repoURL is a GitHub path like "owner/repo" or full URL.
// Fetches all pages (100 tags per page).
func FetchAllGithubTags(repoURL string) (map[string]string, error) {
	baseRef, _ := SplitGitHubComponent(repoURL)
	owner, repo, _ := fetchRepositorySegments(baseRef)
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
