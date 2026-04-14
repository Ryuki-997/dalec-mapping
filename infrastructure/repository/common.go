package repository

import (
	"net/url"
	"path"
	"regexp"
	"strings"
)

// TagInfo pairs a tag name with the commit SHA it points to.
type TagInfo struct {
	Name   string // Tag name, e.g. "v1.2.3"
	Commit string // Commit SHA the tag points to
}

// semverTagRe matches the first vX.Y.Z occurrence inside any tag string.
var semverTagRe = regexp.MustCompile(`v(\d+)\.(\d+)\.(\d+)`)

// IsADORepo returns true when the repository URL points to Azure DevOps.
func IsADORepo(repoURL string) bool {
	normalized := strings.TrimPrefix(strings.TrimPrefix(repoURL, "https://"), "http://")
	return strings.HasPrefix(normalized, "dev.azure.com/") ||
		strings.HasPrefix(normalized, "ssh.dev.azure.com/") ||
		strings.Contains(normalized, ".visualstudio.com/")
}

// SplitComponent splits a repository reference into its base git-addressable
// URL/path and an optional component subdirectory appended by the user.
//
// ADO example:
//
//	"https://dev.azure.com/org/project/_git/repo/test/npd"
//	→ baseURL  = "https://dev.azure.com/org/project/_git/repo"
//	  component = "test/npd"
//
// GitHub example:
//
//	"owner/repo/test/npd"
//	→ baseURL  = "owner/repo"
//	  component = "test/npd"
//
// When there is no component path, component is "".
func SplitComponent(repoRef string) (baseURL, component string) {
	if IsADORepo(repoRef) {
		return splitADOComponent(repoRef)
	}
	return splitGitHubComponent(repoRef)
}

// splitADOComponent handles ADO URLs. Everything after _git/<repo> is component.
func splitADOComponent(repoURL string) (string, string) {
	repoURL = strings.TrimSuffix(repoURL, ".git")
	u, err := url.Parse(repoURL)
	if err != nil {
		return repoURL, ""
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	for i, p := range parts {
		if p == "_git" && i+1 < len(parts) {
			// _git/<repo> is parts[i] and parts[i+1]
			// Everything after parts[i+1] is component path.
			baseParts := parts[:i+2]
			u.Path = "/" + strings.Join(baseParts, "/")
			baseURL := u.String()
			if i+2 < len(parts) {
				return baseURL, strings.Join(parts[i+2:], "/")
			}
			return baseURL, ""
		}
	}
	// visualstudio.com format: {org}.visualstudio.com/{project}/_git/{repo}/...
	// Same logic — _git marker already handled above.
	return repoURL, ""
}

// splitGitHubComponent handles GitHub paths (owner/repo or owner/repo/component/...).
// Also handles full https://github.com/owner/repo/... URLs.
func splitGitHubComponent(repoRef string) (string, string) {
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

// ComponentName returns the leaf directory name from a component path.
// Returns "" when componentPath is empty.
func ComponentName(componentPath string) string {
	if componentPath == "" {
		return ""
	}
	return path.Base(componentPath)
}

// ResolveFilePath resolves a file path (from onboard.yml) relative to a
// component directory. Supports "../" traversal for files outside the component.
// When componentPath is empty the filePath is returned unchanged.
func ResolveFilePath(componentPath, filePath string) string {
	if componentPath == "" || filePath == "" {
		return filePath
	}
	return path.Clean(componentPath + "/" + filePath)
}
