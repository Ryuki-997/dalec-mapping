package github

// ─── Chunk 1 · URL PARSING ──────────────────────────────────────────────────

import "strings"

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
