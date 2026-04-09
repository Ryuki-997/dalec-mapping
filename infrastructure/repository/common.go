package repository

import (
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
