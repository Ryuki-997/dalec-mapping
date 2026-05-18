package ado

import (
	"path"
)

// ResolveComponentPath determines the component subdirectory within a repo
// using a prioritized fallback chain:
//  1. URL suffix — the trailing path after _git/<repo> in the ADO URL
//  2. Dockerfile directory — strip the "Dockerfile" leaf
//  3. Makefile directory — strip the "Makefile" leaf
//  4. Component name — falls back to the onboard key name
func ResolveComponentPath(repoURL, dockerfileDir, makefileDir, componentName string) string {
	baseURL, urlComponent := SplitADOComponent(repoURL)
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

	repoName := adoRepoName(baseURL)
	if componentName == repoName {
		return "."
	}
	return componentName
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
