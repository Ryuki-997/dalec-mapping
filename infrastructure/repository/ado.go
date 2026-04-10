package repository

// ═══════════════════════════════════════════════════════════════════════════════
// ado.go — Azure DevOps repository access via the local git checkout.
//
// The ADO pipeline checks out partner repositories via resource declarations:
//
//   resources:
//     repositories:
//       - repository: aks-vm-extension          # alias → checkout dir name
//         type: git
//         name: CloudNativeCompute/aks-vm-extension
//         fetchDepth: 0
//         fetchTags: true
//
// Each repo is checked out to $(Pipeline.Workspace)/<alias>, which is
// available as the environment variable PIPELINE_WORKSPACE.  The alias
// matches the repo name in the resource "name:" field, so the local path is
// derived directly from the ADO repository URL — no PAT or ADO_TOKEN needed.
//
// Public surface (same signatures as the old API-based version):
//
//   FetchAllADOTags(repoURL)                   → []TagInfo
//   FetchADOTagCommit(repoURL, tag)             → string
//   FetchADOFileContent(repoURL, filePath, tag) → []byte
//   FetchADORepoInfo(repoURL, subdir, tag)      → *RepoInfo
// ═══════════════════════════════════════════════════════════════════════════════

import (
	"bytes"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	domainRepo "dalec-mapping/domain/repository"
)

// repoNameFromURL extracts the repository name (last path segment after _git/)
// from an ADO URL.  Falls back to the last non-empty path component.
func repoNameFromURL(repoURL string) string {
	repoURL = strings.TrimSuffix(repoURL, ".git")
	u, err := url.Parse(repoURL)
	if err != nil {
		return filepath.Base(repoURL)
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	for i, p := range parts {
		if p == "_git" && i+1 < len(parts) {
			return parts[i+1]
		}
	}
	// Fallback: last non-empty segment
	for i := len(parts) - 1; i >= 0; i-- {
		if parts[i] != "" {
			return parts[i]
		}
	}
	return filepath.Base(repoURL)
}

// localCheckoutDir returns the absolute path to the local git checkout for the
// given ADO repository URL: $PIPELINE_WORKSPACE/<repoName>.
func localCheckoutDir(repoURL string) (string, error) {
	ws := os.Getenv("PIPELINE_WORKSPACE")
	if ws == "" {
		return "", fmt.Errorf("PIPELINE_WORKSPACE is not set")
	}
	return filepath.Join(ws, repoNameFromURL(repoURL)), nil
}

// gitText runs a git command in dir and returns trimmed stdout as a string.
func gitText(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w — %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimRight(string(out), "\n"), nil
}

// gitBytes runs a git command in dir and returns raw stdout bytes.
func gitBytes(dir string, args ...string) ([]byte, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git %s: %w — %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

// FetchAllADOTags returns all semver tags and their commit SHAs from the local
// checkout of the repository at repoURL.
func FetchAllADOTags(repoURL string) ([]TagInfo, error) {
	dir, err := localCheckoutDir(repoURL)
	if err != nil {
		return nil, err
	}

	// for-each-ref with conditional: use peeled objectname (commit) for annotated
	// tags, otherwise use objectname (already a commit for lightweight tags).
	out, err := gitText(dir,
		"for-each-ref",
		"--format=%(if)%(*objectname)%(then)%(*objectname)%(else)%(objectname)%(end) %(refname:short)",
		"refs/tags",
	)
	if err != nil {
		return nil, fmt.Errorf("failed to list git tags in %s: %w", dir, err)
	}

	var tags []TagInfo
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		sp := strings.SplitN(line, " ", 2)
		if len(sp) != 2 {
			continue
		}
		sha, name := sp[0], sp[1]
		if !semverTagRe.MatchString(name) {
			continue
		}
		tags = append(tags, TagInfo{Name: name, Commit: sha})
	}
	return tags, nil
}

// FetchADOTagCommit resolves a git tag to its commit SHA in the local checkout.
// The ^{} suffix dereferences annotated tag objects to the underlying commit.
func FetchADOTagCommit(repoURL, tag string) (string, error) {
	dir, err := localCheckoutDir(repoURL)
	if err != nil {
		return "", err
	}
	return gitText(dir, "rev-parse", tag+"^{}")
}

// FetchADOFileContent returns the raw bytes of a file at the given tag from the
// local checkout.  filePath should be repo-relative (e.g. "Dockerfile").
func FetchADOFileContent(repoURL, filePath, tag string) ([]byte, error) {
	dir, err := localCheckoutDir(repoURL)
	if err != nil {
		return nil, err
	}
	filePath = strings.TrimPrefix(filePath, "/")
	return gitBytes(dir, "show", tag+":"+filePath)
}

// FetchADORepoInfo builds a RepoInfo from the local git checkout metadata.
func FetchADORepoInfo(repoURL, subdir, tag string) (*domainRepo.RepoInfo, error) {
	dir, err := localCheckoutDir(repoURL)
	if err != nil {
		return nil, err
	}

	repoName := filepath.Base(dir)

	// Default branch: try symbolic-ref on origin/HEAD (works even in detached HEAD).
	branch := "main"
	if b, err := gitText(dir, "symbolic-ref", "--short", "refs/remotes/origin/HEAD"); err == nil {
		branch = strings.TrimPrefix(b, "origin/")
	} else if b, err := gitText(dir, "rev-parse", "--abbrev-ref", "HEAD"); err == nil && b != "HEAD" {
		branch = b
	}

	// Remote URL for the GitURL field.
	gitURL := repoURL
	if u, err := gitText(dir, "remote", "get-url", "origin"); err == nil {
		gitURL = u
	}

	// Extract org/project from the remote URL for the Owner field (best-effort).
	org := ""
	if parts := strings.Split(strings.Trim(func() string {
		u, _ := url.Parse(strings.TrimSuffix(gitURL, ".git"))
		if u != nil {
			return u.Path
		}
		return ""
	}(), "/"), "/"); len(parts) >= 4 && parts[2] == "_git" {
		org = parts[0] // dev.azure.com/{org}/...
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
