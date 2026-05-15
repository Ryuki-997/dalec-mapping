package repository

// ═══════════════════════════════════════════════════════════════════════════════
// ado.go — Azure DevOps repository access via git commands.
//
//   Chunk 1 · URL PARSING              IsADORepo(), SplitADOComponent()
//   Chunk 2 · GIT CLIENT               adoAuthURL(), gitOut(), gitOutBytes()
//   Chunk 3 · REPOSITORY INFO          FetchADORepoInfo(), adoRepoName(), adoOrg()
//   Chunk 4 · TAGS                     FetchAllADOTags()
//   Chunk 5 · FILE CONTENT             FetchADOFileContent()
// ═══════════════════════════════════════════════════════════════════════════════

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"net/url"
	"os"
	"os/exec"
	"path"
	"strings"
	"sync"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"

	domainRepo "dalec-mapping/domain/repository"
	"dalec-mapping/pipeline"
)

// ─── Chunk 1 · URL PARSING ──────────────────────────────────────────────────

// IsADORepo returns true when the repository URL points to Azure DevOps.
func IsADORepo(repoURL string) bool {
	normalized := strings.TrimPrefix(strings.TrimPrefix(repoURL, "https://"), "http://")
	return strings.HasPrefix(normalized, "dev.azure.com/") ||
		strings.HasPrefix(normalized, "ssh.dev.azure.com/") ||
		strings.Contains(normalized, ".visualstudio.com/")
}

// SplitADOComponent splits an ADO repository URL into its base git-addressable
// URL and an optional component subdirectory. Everything after _git/<repo> is
// treated as the component path.
//
// Example:
//
//	"https://dev.azure.com/org/project/_git/repo/test/npd"
//	→ baseURL  = "https://dev.azure.com/org/project/_git/repo"
//	  component = "test/npd"
func SplitADOComponent(repoURL string) (string, string) {
	repoURL = strings.TrimSuffix(repoURL, ".git")
	u, err := url.Parse(repoURL)
	if err != nil {
		return repoURL, ""
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	for i, p := range parts {
		if p == "_git" && i+1 < len(parts) {
			baseParts := parts[:i+2]
			u.Path = "/" + strings.Join(baseParts, "/")
			baseURL := u.String()
			if i+2 < len(parts) {
				return baseURL, strings.Join(parts[i+2:], "/")
			}
			return baseURL, ""
		}
	}
	return repoURL, ""
}

// ─── Chunk 2 · GIT CLIENT ───────────────────────────────────────────────────

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

// ─── Chunk 3 · REPOSITORY INFO ──────────────────────────────────────────────

// FetchADORepoInfo assembles a RepoInfo by querying the ADO repository
// remotely via git ls-remote. The repoURL may contain a component path
// (e.g. _git/repo/comp/path) which is extracted and stored on RepoInfo.
// The license SPDX identifier is read from pipeline.Current.Onboard.License;
// when empty, defaults to "proprietary".
func FetchADORepoInfo(repoURL string) (*domainRepo.RepoInfo, error) {
	baseURL, componentPath := SplitADOComponent(repoURL)

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

	componentName := ""
	if componentPath != "" {
		componentName = path.Base(componentPath)
	}

	license := pipeline.Current.Onboard.License
	if license == "" {
		license = "proprietary"
	}

	info := &domainRepo.RepoInfo{
		Owner:         adoOrg(baseURL),
		Repo:          adoRepoName(baseURL),
		Branch:        branch,
		ComponentPath: componentPath,
		ComponentName: componentName,
		GitURL:        baseURL,
		Description:   fmt.Sprintf("This is the %s project.", adoRepoName(baseURL)),
		License:       license,
	}

	return info, nil
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

// ─── Chunk 4 · TAGS ────────────────────────────────────────────────────────

// FetchAllADOTags lists all tags and their commit SHAs from the remote
// ADO repository using git ls-remote. Returns a map of tagName → commitSHA.
func FetchAllADOTags(repoURL string) (map[string]string, error) {
	baseURL, _ := SplitADOComponent(repoURL)
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

	tags := make(map[string]string, len(byName))
	for name, e := range byName {
		commit := e.peeled
		if commit == "" {
			commit = e.plain
		}
		tags[name] = commit
	}
	return tags, nil
}

// ─── Chunk 5 · FILE CONTENT ─────────────────────────────────────────────────

// FetchADOFileContent fetches a single file at the given tag from the ADO
// repository using a temporary partial clone (--filter=blob:none).
// The clone fetches only the tree objects; git show lazily fetches the one
// blob needed. When the exact path fails (e.g. the path points to a directory
// or is one level too shallow), it falls back to git ls-tree to discover the
// target filename under the parent directory.
func FetchADOFileContent(repoURL, filePath, tag string) ([]byte, error) {
	baseURL, _ := SplitADOComponent(repoURL)
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

	content, err := gitOutBytes(tmpDir, "show", "HEAD:"+filePath)
	if err == nil {
		return content, nil
	}

	// Exact path failed — search the parent directory for the target filename
	// (handles repos that nest files one level deeper, e.g. docker/<component>/Dockerfile).
	dir := path.Dir(filePath)
	fileName := path.Base(filePath)
	resolvedPath, findErr := findFileInTree(tmpDir, dir, fileName)
	if findErr != nil {
		return nil, err
	}
	log.Printf("  Resolved %s → %s\n", filePath, resolvedPath)
	return gitOutBytes(tmpDir, "show", "HEAD:"+resolvedPath)
}

// findFileInTree searches a directory tree for a file by name using git ls-tree.
// Returns the first path whose base name matches fileName.
func findFileInTree(repoDir, dir, fileName string) (string, error) {
	out, err := gitOut(repoDir, "ls-tree", "-r", "--name-only", "HEAD", dir)
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if path.Base(line) == fileName {
			return line, nil
		}
	}
	return "", fmt.Errorf("file %s not found under %s", fileName, dir)
}

// ─── Chunk 6 · GENERATOR DETECTION ──────────────────────────────────────────

// DetectADOGenerator probes the ADO repository at the given tag for known
// build-system marker files (go.mod, Cargo.toml, etc.) and returns the
// detected SourceGenerator. When componentPath is set, the probe is scoped
// to that subdirectory; otherwise markers are probed at the repo root.
// Returns an empty SourceGenerator if no marker is found.
func DetectADOGenerator(repoURL, componentPath, tag string) domainRepo.SourceGenerator {
	for _, marker := range domainRepo.FileGeneratorMarkers {
		markerPath := marker.FileName
		if componentPath != "" {
			markerPath = componentPath + "/" + marker.FileName
		}

		if _, err := FetchADOFileContent(repoURL, markerPath, tag); err == nil {
			log.Printf("  Detected generator %s via %s\n", marker.Generator, markerPath)
			return marker.Generator
		}
	}
	return ""
}


