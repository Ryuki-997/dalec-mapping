package github

import (
	"path"
	"strings"
)

// ResolveComponentPath determines the component subdirectory within a repo
// using a prioritized fallback chain:
//  1. URL suffix — the trailing path after owner/repo in the GitHub path
//  2. Dockerfile directory — strip the "Dockerfile" leaf
//  3. Makefile directory — strip the "Makefile" leaf
//  4. Component name — falls back to the onboard key name
func ResolveComponentPath(repoURL, dockerfileDir, makefileDir, componentName string) string {
	baseRef, urlComponent := SplitGitHubComponent(repoURL)
	if urlComponent != "" {
		return urlComponent
	}

	if dockerfileDir != "" {
		if cp := componentPathFromDir(dockerfileDir, "Dockerfile"); cp != "" {
			return cp
		}
	}

	if makefileDir != "" {
		if cp := componentPathFromDir(makefileDir, "Makefile"); cp != "" {
			return cp
		}
	}

	repoName := repoNameFromBaseRef(baseRef)
	if componentName == repoName {
		return "."
	}
	return componentName
}

// repoNameFromBaseRef extracts the repository name (last segment) from a
// GitHub base ref like "owner/repo" or "https://github.com/owner/repo".
func repoNameFromBaseRef(baseRef string) string {
	baseRef = strings.TrimPrefix(baseRef, "https://github.com/")
	baseRef = strings.TrimPrefix(baseRef, "http://github.com/")
	parts := strings.Split(strings.Trim(baseRef, "/"), "/")
	if len(parts) >= 2 {
		return parts[len(parts)-1]
	}
	return baseRef
}

// componentPathFromDir extracts the component path from a user-provided
// directory or file path by stripping the build-file leaf when present.
//
//	""                   → ""
//	"."                  → "."
//	"cns"               → "cns"
//	"docker/Dockerfile" → "docker"
//	"Dockerfile"        → "."
func componentPathFromDir(dirPath, filename string) string {
	if dirPath == "" {
		return ""
	}
	if dirPath == "." {
		return "."
	}

	baseName := path.Base(dirPath)
	if baseName == filename {
		parent := path.Dir(dirPath)
		if parent == "." {
			return "."
		}
		return parent
	}

	return dirPath
}
