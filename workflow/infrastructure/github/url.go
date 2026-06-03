package github

// ─── Chunk 1 · URL PARSING ──────────────────────────────────────────────────

import (
	"fmt"
	"strings"
)

// RepoAPIPath builds a GitHub REST path of the form
// "repos/<owner>/<repo>/<suffix>". suffixFmt is treated as a fmt format
// string applied to args (use a plain string when no args are needed).
//
// Example:
//
//	RepoAPIPath("foo", "bar", "git/trees/%s?recursive=1", branch)
//	→ "repos/foo/bar/git/trees/main?recursive=1"
func RepoAPIPath(owner, repo, suffixFmt string, args ...any) string {
	base := "repos/" + owner + "/" + repo
	if suffixFmt == "" {
		return base
	}
	return base + "/" + fmt.Sprintf(suffixFmt, args...)
}

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

	// Also handle owner/repo/tree/branch[/subpath...] format.
	parts := strings.Split(strings.Trim(repoRef, "/"), "/")
	if len(parts) >= 4 && parts[2] == "tree" {
		// owner/repo/tree/branch[/subpath...]
		base := prefix + parts[0] + "/" + parts[1]
		if len(parts) > 4 {
			return base, strings.Join(parts[4:], "/")
		}
		return base, ""
	}
	if len(parts) <= 2 {
		return prefix + repoRef, ""
	}
	base := prefix + parts[0] + "/" + parts[1]
	return base, strings.Join(parts[2:], "/")
}
