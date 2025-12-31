package github

import (
	"strings"
)

// ToRepoMetadata converts RepoInfo to a generic metadata interface
// This allows the transformer package to use GitHub data without depending on it
func (info *RepoInfo) ToRepoMetadata() map[string]interface{} {
	return map[string]interface{}{
		"GitURL":      info.GitURL,
		"Commit":      info.LatestCommit,
		"Website":     info.Website,
		"Description": info.Description,
		"License":     info.License,
		"RepoName":    info.Repo,
	}
}

// DerivePackageName extracts a clean package name from the repository
func (info *RepoInfo) DerivePackageName() string {
	name := info.Repo

	// Clean up common suffixes
	name = strings.TrimSuffix(name, "-docker")
	name = strings.TrimSuffix(name, "-container")

	// Convert to lowercase
	name = strings.ToLower(name)

	return name
}

// DeriveSourceName creates a source name from repository info
func (info *RepoInfo) DeriveSourceName() string {
	// Use repo name as source name
	return info.Repo
}

// GetCloneURL returns the HTTPS clone URL
func (info *RepoInfo) GetCloneURL() string {
	return info.GitURL
}

// IsGoProject checks if this appears to be a Go project
func (info *RepoInfo) IsGoProject() bool {
	// This would require additional API calls to check for go.mod
	// For now, return false - the Dockerfile parser will detect it
	return false
}

// GetWorkdirName derives a workdir name from the repo
func (info *RepoInfo) GetWorkdirName() string {
	return "/" + info.Repo
}

// GetBinaryName derives likely binary name from repo name
func (info *RepoInfo) GetBinaryName() string {
	name := info.Repo

	// Clean up common prefixes/suffixes
	name = strings.TrimPrefix(name, "go-")
	name = strings.TrimSuffix(name, "-go")
	name = strings.ToLower(name)

	return name
}

// FormatForDisplay returns a nicely formatted repository reference
func (info *RepoInfo) FormatForDisplay() string {
	return info.FullName
}
