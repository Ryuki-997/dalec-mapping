package ado

// ─── Chunk 3 · REPOSITORY INFO ──────────────────────────────────────────────

import (
	"fmt"
	"net/url"
	"strings"

	domainRepo "dalec-mapping/domain/repository"
)

// FetchADORepoInfo assembles a RepoInfo by querying the ADO repository
// remotely via git ls-remote. The repoURL may contain a component path
// (e.g. _git/repo/comp/path) which is extracted and stored on RepoInfo.
// ADO has no license API, so the returned license is always "" and callers
// apply their own fallback.
func FetchADORepoInfo(repoURL string) (*domainRepo.RepoInfo, string, error) {
	baseURL, componentPath := SplitADOComponent(repoURL)

	// Resolve default branch from the symbolic ref of HEAD.
	branch := "main"
	if out, err := gitOut("", "ls-remote", "--symref", adoAuthURL(baseURL), "HEAD"); err == nil {
		for _, line := range strings.Split(out, "\n") {
			if strings.HasPrefix(line, "ref: refs/heads/") {
				fields := strings.Fields(line)
				branch = strings.TrimPrefix(fields[0], "ref: refs/heads/")
				break
			}
		}
	}

	info := &domainRepo.RepoInfo{
		Owner:         adoOrg(baseURL),
		Repo:          adoRepoName(baseURL),
		Branch:        branch,
		ComponentPath: componentPath,
		GitURL:        baseURL,
		Description:   fmt.Sprintf("This is the %s project.", adoRepoName(baseURL)),
	}

	return info, "", nil
}

// adoRepoName extracts the repository name from an ADO git URL
// (the path segment after "_git/").
func adoRepoName(repoURL string) string {
	repoURL = strings.TrimSuffix(repoURL, ".git")
	u, err := url.Parse(repoURL)
	if err != nil {
		return lastSegment(repoURL)
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	for i, p := range parts {
		if p == "_git" && i+1 < len(parts) {
			return parts[i+1]
		}
	}
	return lastSegment(repoURL)
}

// adoOrg extracts the org name from an ADO URL (best-effort).
func adoOrg(repoURL string) string {
	repoURL = strings.TrimSuffix(repoURL, ".git")
	u, err := url.Parse(repoURL)
	if err != nil {
		return ""
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	// dev.azure.com/{org}/{project}/_git/{repo}
	if len(parts) >= 4 && parts[2] == "_git" {
		return parts[0]
	}
	// {org}.visualstudio.com/...
	if strings.Contains(u.Hostname(), ".visualstudio.com") {
		return strings.TrimSuffix(u.Hostname(), ".visualstudio.com")
	}
	return ""
}

func lastSegment(s string) string {
	s = strings.TrimRight(s, "/")
	if i := strings.LastIndex(s, "/"); i >= 0 {
		return s[i+1:]
	}
	return s
}
