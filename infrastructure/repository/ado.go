package repository

// ═══════════════════════════════════════════════════════════════════════════════
// ado.go — Azure DevOps tag fetching via the ADO REST API.
//
//   FetchAllADOTags(repoURL) → []TagInfo
//     Fetches all semver git tags from an ADO repository and returns each
//     tag name paired with its commit SHA.
//
//   Authentication: ADO_TOKEN environment variable (PAT or Bearer token).
//   ADO API version: 7.1
// ═══════════════════════════════════════════════════════════════════════════════

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	domainRepo "dalec-mapping/domain/repository"
)

// parseADORepoURL extracts org, project, and repo from an ADO git URL.
// Supports:
//   https://dev.azure.com/{org}/{project}/_git/{repo}
//   https://{org}.visualstudio.com/{project}/_git/{repo}
//   https://{org}.visualstudio.com/_git/{repo}[/...]
func parseADORepoURL(repoURL string) (org, project, repo string, err error) {
	repoURL = strings.TrimSuffix(repoURL, ".git")
	u, parseErr := url.Parse(repoURL)
	if parseErr != nil {
		return "", "", "", fmt.Errorf("invalid ADO URL %q: %w", repoURL, parseErr)
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")

	// dev.azure.com/{org}/{project}/_git/{repo}  → ≥4 parts, org in path
	if len(parts) >= 4 && parts[2] == "_git" {
		return parts[0], parts[1], parts[3], nil
	}

	// {org}.visualstudio.com/{project}/_git/{repo}  → ≥3 parts, org in subdomain
	if len(parts) >= 3 && parts[1] == "_git" {
		org := strings.TrimSuffix(u.Hostname(), ".visualstudio.com")
		return org, parts[0], parts[2], nil
	}

	// {org}.visualstudio.com/_git/{repo}[/...]  → no project segment; project defaults to repo name.
	// Any trailing path components (e.g. "/tags") are UI browse paths and are ignored.
	if len(parts) >= 2 && parts[0] == "_git" {
		org := strings.TrimSuffix(u.Hostname(), ".visualstudio.com")
		repo := parts[1]
		return org, repo, repo, nil
	}

	return "", "", "", fmt.Errorf("unexpected ADO URL path %q: expected /{org}/{project}/_git/{repo}, {org}.visualstudio.com/{project}/_git/{repo}, or {org}.visualstudio.com/_git/{repo}", u.Path)
}

// makeADORequest performs an authenticated GET to the ADO REST API.
// Retries up to 3 times on 429 responses, honouring the Retry-After header when present.
func makeADORequest(apiURL string, accept string) ([]byte, error) {
	const maxRetries = 3
	backoff := 2 * time.Second

	for attempt := 0; attempt <= maxRetries; attempt++ {
		req, err := http.NewRequest("GET", apiURL, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to create ADO request: %w", err)
		}
		if token := os.Getenv("ADO_TOKEN"); token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		req.Header.Set("Accept", accept)

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("ADO request failed: %w", err)
		}

		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("failed to read ADO response: %w", err)
		}

		if resp.StatusCode == http.StatusTooManyRequests && attempt < maxRetries {
			wait := backoff * (1 << attempt) // 2s, 4s, 8s
			if ra := resp.Header.Get("Retry-After"); ra != "" {
				if secs, err := strconv.Atoi(ra); err == nil {
					wait = time.Duration(secs) * time.Second
				}
			}
			log.Printf("⏳ ADO rate-limited (429); retrying in %s (attempt %d/%d)\n", wait, attempt+1, maxRetries)
			time.Sleep(wait)
			continue
		}

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, fmt.Errorf("ADO API returned status %d: %s", resp.StatusCode, string(body))
		}
		return body, nil
	}
	return nil, fmt.Errorf("ADO API rate limit exceeded after %d retries", maxRetries)
}

// adoRepoAPIBase returns the base URL for ADO git-repository API calls.
// All ADO organisations are reachable via dev.azure.com regardless of whether
// the original clone URL used dev.azure.com or {org}.visualstudio.com.
func adoRepoAPIBase(org, project, repo string) string {
	return fmt.Sprintf(
		"https://dev.azure.com/%s/%s/_apis/git/repositories/%s",
		url.PathEscape(org), url.PathEscape(project), url.PathEscape(repo),
	)
}

// FetchAllADOTags fetches all semver git tags from an ADO repository.
// repoURL must be an ADO git URL: https://dev.azure.com/{org}/{project}/_git/{repo}
// Returns each semver tag paired with its commit SHA, paginating until all refs are fetched.
func FetchAllADOTags(repoURL string) ([]TagInfo, error) {
	org, project, repo, err := parseADORepoURL(repoURL)
	if err != nil {
		return nil, err
	}

	// GET {base}/refs?filter=tags/&api-version=7.1
	baseURL := adoRepoAPIBase(org, project, repo) + "/refs?filter=tags/&api-version=7.1"

	var tags []TagInfo
	continuationToken := ""

	for {
		apiURL := baseURL
		if continuationToken != "" {
			apiURL += "&continuationToken=" + url.QueryEscape(continuationToken)
		}

		body, err := makeADORequest(apiURL, "application/json")
		if err != nil {
			return nil, fmt.Errorf("failed to fetch ADO tags for %s/%s/%s: %w", org, project, repo, err)
		}

		var result struct {
			Value []struct {
				Name           string `json:"name"`
				ObjectID       string `json:"objectId"`
				PeeledObjectID string `json:"peeledObjectId"`
			} `json:"value"`
			Count int `json:"count"`
		}
		if err := json.Unmarshal(body, &result); err != nil {
			return nil, fmt.Errorf("failed to parse ADO tags response: %w", err)
		}

		for _, ref := range result.Value {
			// ref.Name is e.g. "refs/tags/v1.2.3" or "refs/tags/azure-cns/v1.2.3"
			name := strings.TrimPrefix(ref.Name, "refs/tags/")
			if !semverTagRe.MatchString(name) {
				continue
			}
			// peeledObjectId is the actual commit for annotated tags; objectId for lightweight tags.
			commit := ref.PeeledObjectID
			if commit == "" {
				commit = ref.ObjectID
			}
			tags = append(tags, TagInfo{Name: name, Commit: commit})
		}

		// ADO paginates at 100 refs per page when $top is not set.
		if result.Count < 100 {
			break
		}
		if len(result.Value) > 0 {
			continuationToken = result.Value[len(result.Value)-1].ObjectID
		} else {
			break
		}
	}

	return tags, nil
}

// FetchADOTagCommit resolves a git tag to its commit SHA for the given ADO repository URL.
func FetchADOTagCommit(repoURL, tag string) (string, error) {
	org, project, repo, err := parseADORepoURL(repoURL)
	if err != nil {
		return "", err
	}

	apiURL := adoRepoAPIBase(org, project, repo) +
		fmt.Sprintf("/commits?searchCriteria.itemVersion.version=%s&searchCriteria.itemVersion.versionType=tag&$top=1&api-version=7.1",
			url.QueryEscape(tag),
		)

	body, err := makeADORequest(apiURL, "application/json")
	if err != nil {
		return "", fmt.Errorf("failed to fetch commit for ADO tag %q: %w", tag, err)
	}

	var result struct {
		Value []struct {
			CommitID string `json:"commitId"`
		} `json:"value"`
		Count int `json:"count"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("failed to parse ADO commits response: %w", err)
	}
	if len(result.Value) == 0 {
		return "", fmt.Errorf("no commit found for ADO tag %q", tag)
	}
	return result.Value[0].CommitID, nil
}

// FetchADOFileContent fetches the raw content of a file from an ADO repository at the given tag.
func FetchADOFileContent(repoURL, filePath, tag string) ([]byte, error) {
	org, project, repo, err := parseADORepoURL(repoURL)
	if err != nil {
		return nil, err
	}

	apiURL := adoRepoAPIBase(org, project, repo) +
		fmt.Sprintf("/items?path=%s&versionDescriptor.version=%s&versionDescriptor.versionType=tag&api-version=7.1",
			url.QueryEscape(filePath), url.QueryEscape(tag),
		)
	return makeADORequest(apiURL, "application/octet-stream")
}

// FetchADORepoInfo fetches repository metadata from ADO and returns a populated RepoInfo.
func FetchADORepoInfo(repoURL, subdir, tag string) (*domainRepo.RepoInfo, error) {
	org, project, repoName, err := parseADORepoURL(repoURL)
	if err != nil {
		return nil, err
	}

	apiURL := adoRepoAPIBase(org, project, repoName) + "?api-version=7.1"

	body, err := makeADORequest(apiURL, "application/json")
	if err != nil {
		return nil, fmt.Errorf("failed to fetch ADO repo info: %w", err)
	}

	var repoData struct {
		Name          string `json:"name"`
		RemoteURL     string `json:"remoteUrl"`
		DefaultBranch string `json:"defaultBranch"`
	}
	if err := json.Unmarshal(body, &repoData); err != nil {
		return nil, fmt.Errorf("failed to parse ADO repo info: %w", err)
	}

	branch := strings.TrimPrefix(repoData.DefaultBranch, "refs/heads/")
	if branch == "" {
		branch = "main"
	}
	gitURL := repoData.RemoteURL
	if gitURL == "" {
		gitURL = repoURL
	}

	info := &domainRepo.RepoInfo{
		Owner:       org,
		Repo:        repoName,
		Branch:      branch,
		Subdir:      subdir,
		GitURL:      gitURL,
		Description: fmt.Sprintf("This is the %s project.", repoName),
	}

	if tag != "" {
		commit, err := FetchADOTagCommit(repoURL, tag)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve commit for tag %s: %w", tag, err)
		}
		info.LatestCommit = commit
		if m := semverTagRe.FindString(tag); m != "" {
			info.Version = strings.TrimPrefix(m, "v")
		} else {
			info.Version = strings.TrimPrefix(tag, "v")
		}
	}

	return info, nil
}
