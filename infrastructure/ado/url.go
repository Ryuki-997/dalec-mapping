package ado

// ─── Chunk 1 · URL PARSING ──────────────────────────────────────────────────

import (
	"net/url"
	"strings"
)

// IsADORepo returns true when the repository URL points to Azure DevOps.
func IsADORepo(repoURL string) bool {
	normalized := strings.TrimPrefix(strings.TrimPrefix(repoURL, "https://"), "http://")
	return strings.HasPrefix(normalized, "dev.azure.com/") ||
		strings.HasPrefix(normalized, "ssh.dev.azure.com/") ||
		strings.Contains(normalized, ".visualstudio.com/")
}

// SplitADOComponent splits an ADO repository URL into its base git-addressable
// URL and an optional component subdirectory. Everything after _git/<repo> is
// treated as the component path.
//
// Example:
//
//	"https://dev.azure.com/org/project/_git/repo/test/npd"
//	→ baseURL  = "https://dev.azure.com/org/project/_git/repo"
//	  component = "test/npd"
func SplitADOComponent(repoURL string) (string, string) {
	repoURL = strings.TrimSuffix(repoURL, ".git")
	u, err := url.Parse(repoURL)
	if err != nil {
		return repoURL, ""
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	for i, p := range parts {
		if p == "_git" && i+1 < len(parts) {
			baseParts := parts[:i+2]
			u.Path = "/" + strings.Join(baseParts, "/")
			baseURL := u.String()
			if i+2 < len(parts) {
				return baseURL, strings.Join(parts[i+2:], "/")
			}
			return baseURL, ""
		}
	}
	return repoURL, ""
}
