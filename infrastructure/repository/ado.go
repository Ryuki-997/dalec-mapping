package repository

// ═══════════════════════════════════════════════════════════════════════════════
// ado.go — Azure DevOps repository access via git remote commands.
//
// All operations run git against the ADO repository URL directly; no local
// checkout is required.  Authentication uses a short-lived Entra ID access
// token acquired via azidentity.  On AKS the workload
// identity webhook supplies credentials automatically; for local development
// `az login` is sufficient.
//
// Repository URLs may include a component path appended after the repo name:
//   https://dev.azure.com/org/project/_git/repo/component/path
// All public functions accept the full URL and call SplitComponent() internally
// to extract the base git URL before running git commands.
//
//   FetchAllADOTags(repoURL)                   → []TagInfo   (git ls-remote)
//   FetchADOTagCommit(repoURL, tag)             → string      (git ls-remote)
//   FetchADOFileContent(repoURL, filePath, tag) → []byte      (sparse clone)
//   FetchADORepoInfo(repoURL, tag)              → *RepoInfo   (git ls-remote)
// ═══════════════════════════════════════════════════════════════════════════════

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"

	domainRepo "dalec-mapping/domain/repository"
)

// azureDevOpsScope is the well-known resource ID for Azure DevOps,
// used to request an Entra ID access token with ADO read permissions.
const azureDevOpsScope = "499b84ac-1321-427f-aa17-267ca6975798/.default"

var initCredential = sync.OnceValues(func() (*azidentity.DefaultAzureCredential, error) {
	credential, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create Azure credential: %w", err)
	}
	return credential, nil
})

// cachedADOToken acquires a short-lived Entra ID access token scoped to
// Azure DevOps on the first call and caches it for subsequent calls.
// The token lasts ~1 hour, well beyond a single pipeline run.
// On AKS the WorkloadIdentityCredential is used automatically
// (the webhook injects AZURE_CLIENT_ID, AZURE_TENANT_ID, and
// AZURE_FEDERATED_TOKEN_FILE). For local development AzureCLICredential
// activates via `az login`.
var cachedADOToken = sync.OnceValues(func() (string, error) {
	credential, err := initCredential()
	if err != nil {
		return "", err
	}

	tokenResponse, err := credential.GetToken(context.Background(), policy.TokenRequestOptions{
		Scopes: []string{azureDevOpsScope},
	})
	if err != nil {
		return "", fmt.Errorf("failed to acquire ADO access token: %w", err)
	}

	log.Println("  Acquired Entra ID access token for Azure DevOps")
	return tokenResponse.Token, nil
})

// adoAuthURL injects a short-lived Entra ID access token into the URL as
// HTTP Basic auth so that git subprocesses can authenticate against Azure
// DevOps.  If token acquisition fails the URL is returned unchanged and
// a warning is logged.
func adoAuthURL(rawURL string) string {
	token, err := cachedADOToken()
	if err != nil {
		log.Printf("⚠️  Failed to acquire ADO access token, proceeding without auth: %v", err)
		return rawURL
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
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
	baseURL, _ := SplitComponent(repoURL)
	out, err := gitOut("", "ls-remote", "--tags", adoAuthURL(baseURL))
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
	baseURL, _ := SplitComponent(repoURL)
	out, err := gitOut("", "ls-remote", adoAuthURL(baseURL),
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
	baseURL, _ := SplitComponent(repoURL)
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
		adoAuthURL(baseURL),
		tmpDir,
	); err != nil {
		return nil, fmt.Errorf("failed to clone %s at %s: %w", baseURL, tag, err)
	}

	return gitOutBytes(tmpDir, "show", "HEAD:"+filePath)
}

// FetchADORepoInfo assembles a RepoInfo by querying the ADO repository
// remotely via git ls-remote. The repoURL may contain a component path
// (e.g. _git/repo/comp/path) which is extracted and stored on RepoInfo.
func FetchADORepoInfo(repoURL, tag string) (*domainRepo.RepoInfo, error) {
	baseURL, componentPath := SplitComponent(repoURL)

	// Resolve default branch from the symbolic ref of HEAD.
	branch := "main"
	if out, err := gitOut("", "ls-remote", "--symref", adoAuthURL(baseURL), "HEAD"); err == nil {
		for _, line := range strings.Split(out, "\n") {
			if strings.HasPrefix(line, "ref: refs/heads/") {
				fields := strings.Fields(line)
				branch = strings.TrimPrefix(fields[0], "ref: refs/heads/")
				break
			}
		}
	}

	info := &domainRepo.RepoInfo{
		Owner:         adoOrg(baseURL),
		Repo:          adoRepoName(baseURL),
		Branch:        branch,
		ComponentPath: componentPath,
		ComponentName: ComponentName(componentPath),
		GitURL:        baseURL,
		Description:   fmt.Sprintf("This is the %s project.", adoRepoName(baseURL)),
	}

	if tag != "" {
		commit, err := FetchADOTagCommit(baseURL, tag)
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
