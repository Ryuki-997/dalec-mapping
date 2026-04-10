package repository

// ═══════════════════════════════════════════════════════════════════════════════
// ado.go — Azure DevOps repository access via git remote commands.
//
// All operations run git against the ADO repository URL directly; no local
// checkout is required.  The pipeline agent's credential helper handles
// authentication to msazure.visualstudio.com automatically.
//
//   FetchAllADOTags(repoURL)                   → []TagInfo   (git ls-remote)
//   FetchADOTagCommit(repoURL, tag)             → string      (git ls-remote)
//   FetchADOFileContent(repoURL, filePath, tag) → []byte      (sparse clone)
//   FetchADORepoInfo(repoURL, subdir, tag)      → *RepoInfo   (git ls-remote)
// ═══════════════════════════════════════════════════════════════════════════════

import (
	"bytes"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"strings"

	domainRepo "dalec-mapping/domain/repository"
)

// adoAuthURL injects the ADO_TOKEN into the URL as HTTP Basic auth so that
// git subprocesses can authenticate without a credential helper or terminal
// prompt.  If ADO_TOKEN is not set the URL is returned unchanged.
func adoAuthURL(rawURL string) string {
	token := os.Getenv("ADO_TOKEN")
	if token == "" {
		return rawURL
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	// Basic auth for PATs: empty username, token as password.
	u.User = url.UserPassword("", token)
	return u.String()
}

// gitOut runs git with the given args, optionally inside dir (empty = inherit
// cwd), and returns trimmed stdout.
func gitOut(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w — %s",
			strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimRight(string(out), "\n"), nil
}

// gitOutBytes is like gitOut but returns raw bytes.
func gitOutBytes(dir string, args ...string) ([]byte, error) {
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git %s: %w — %s",
			strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
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

// FetchAllADOTags lists all semver tags and their commit SHAs from the remote
// ADO repository using git ls-remote.
func FetchAllADOTags(repoURL string) ([]TagInfo, error) {
	out, err := gitOut("", "ls-remote", "--tags", adoAuthURL(repoURL))
	if err != nil {
		return nil, fmt.Errorf("failed to list tags for %s: %w", repoURL, err)
	}

	// ls-remote output:
	//   <sha>\trefs/tags/<name>       — lightweight tag or tag object
	//   <sha>\trefs/tags/<name>^{}    — peeled commit (annotated tags only)
	type entry struct{ plain, peeled string }
	byName := map[string]*entry{}

	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) != 2 || !strings.HasPrefix(parts[1], "refs/tags/") {
			continue
		}
		sha := parts[0]
		name := strings.TrimPrefix(parts[1], "refs/tags/")
		peeled := strings.HasSuffix(name, "^{}")
		name = strings.TrimSuffix(name, "^{}")
		if byName[name] == nil {
			byName[name] = &entry{}
		}
		if peeled {
			byName[name].peeled = sha
		} else {
			byName[name].plain = sha
		}
	}

	var tags []TagInfo
	for name, e := range byName {
		commit := e.peeled
		if commit == "" {
			commit = e.plain
		}
		tags = append(tags, TagInfo{Name: name, Commit: commit})
	}
	return tags, nil
}

// FetchADOTagCommit resolves a git tag to its commit SHA via git ls-remote.
// Peeled (annotated tag) SHA is preferred over the raw tag object SHA.
func FetchADOTagCommit(repoURL, tag string) (string, error) {
	out, err := gitOut("", "ls-remote", adoAuthURL(repoURL),
		"refs/tags/"+tag+"^{}",
		"refs/tags/"+tag,
	)
	if err != nil {
		return "", fmt.Errorf("failed to resolve commit for tag %q in %s: %w", tag, repoURL, err)
	}
	// Prefer peeled ref.
	for _, line := range strings.Split(out, "\n") {
		parts := strings.SplitN(strings.TrimSpace(line), "\t", 2)
		if len(parts) == 2 && parts[1] == "refs/tags/"+tag+"^{}" {
			return parts[0], nil
		}
	}
	// Fall back to plain tag ref.
	for _, line := range strings.Split(out, "\n") {
		parts := strings.SplitN(strings.TrimSpace(line), "\t", 2)
		if len(parts) == 2 && parts[1] == "refs/tags/"+tag {
			return parts[0], nil
		}
	}
	return "", fmt.Errorf("tag %q not found in %s", tag, repoURL)
}

// FetchADOFileContent fetches a single file at the given tag from the ADO
// repository using a temporary partial clone (--filter=blob:none).
// The clone fetches only the tree objects; git show lazily fetches the one
// blob needed.
func FetchADOFileContent(repoURL, filePath, tag string) ([]byte, error) {
	filePath = strings.TrimPrefix(filePath, "/")

	tmpDir, err := os.MkdirTemp("", "ado-clone-*")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	if _, err := gitOut("", "clone",
		"--filter=blob:none",
		"--no-checkout",
		"--depth=1",
		"--branch="+tag,
		adoAuthURL(repoURL),
		tmpDir,
	); err != nil {
		return nil, fmt.Errorf("failed to clone %s at %s: %w", repoURL, tag, err)
	}

	return gitOutBytes(tmpDir, "show", "HEAD:"+filePath)
}

// FetchADORepoInfo assembles a RepoInfo by querying the ADO repository
// remotely via git ls-remote.
func FetchADORepoInfo(repoURL, subdir, tag string) (*domainRepo.RepoInfo, error) {
	// Resolve default branch from the symbolic ref of HEAD.
	branch := "main"
	if out, err := gitOut("", "ls-remote", "--symref", adoAuthURL(repoURL), "HEAD"); err == nil {
		for _, line := range strings.Split(out, "\n") {
			if strings.HasPrefix(line, "ref: refs/heads/") {
				fields := strings.Fields(line)
				branch = strings.TrimPrefix(fields[0], "ref: refs/heads/")
				break
			}
		}
	}

	info := &domainRepo.RepoInfo{
		Owner:       adoOrg(repoURL),
		Repo:        adoRepoName(repoURL),
		Branch:      branch,
		Subdir:      subdir,
		GitURL:      repoURL,
		Description: fmt.Sprintf("This is the %s project.", adoRepoName(repoURL)),
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
