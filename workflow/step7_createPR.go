// ═══════════════════════════════════════════════════════════════════════════════
// Step 8 — Create Pull Request
//
//   Creates a feature branch from OnboardBranch, commits specfiles,
//   Dockerfiles, and Makefiles for one or more components, then opens
//   a single PR merging the feature branch into OnboardBranch.
//   Reviewers from the onboarding configs are added to the PR.
//
//   Functions are ordered by call sequence:
//     CreatePR()
//       → deriveFeatureBranch()
//       → createFeatureBranch()
//       → collectFiles()
//           → collectSiblingFiles()
//       → commitAllFiles()
//           → createBlob()
//           → createTree()
//           → createCommit()
//           → updateBranchRef()
//       → buildPRDescription()
//       → createPullRequest()
//       → collectReviewers()
//       → addReviewers()
//       → deleteRemoteBranch()
// ═══════════════════════════════════════════════════════════════════════════════

package workflow

import (
	"encoding/base64"
	"fmt"
	"log"
	"strings"

	"dalec-mapping/domain/naming"
	"dalec-mapping/domain/onboarding"
	repo "dalec-mapping/domain/repository"
	"dalec-mapping/infrastructure/repository"
	"dalec-mapping/utils"
)

// ComponentSpec holds one component's contribution to a PR.
type ComponentSpec struct {
	Onboard     *onboarding.ComponentConfig
	Tag         string
	Revision    int
	SpecContent []byte // generated spec file content
	SpecOnly    bool
	Naming      naming.Naming
}

// PREntry represents a single PR to be created — either for one standalone
// component or for a group of related components.
type PREntry struct {
	GroupName  string
	Components []ComponentSpec
}

// CreatePR creates a feature branch from OnboardBranch, commits all components'
// files to it, and opens a single PR. Works for both standalone components
// (one component) and grouped components (multiple components).
// Returns the PR URL and whether a new PR was created (false if an existing PR was reused).
func CreatePR(entry PREntry) (string, bool, error) {
	if len(entry.Components) == 0 {
		return "", false, fmt.Errorf("no components for %s", entry.GroupName)
	}

	first := entry.Components[0]

	// Check for an existing open PR before creating a new one.
	existingURL, err := findExistingPR(first.Naming.DisplayName, first.Naming.VersionRevision)
	if err != nil {
		log.Printf("⚠️  Failed to check for existing PRs, proceeding with creation: %v", err)
	} else if existingURL != "" {
		log.Printf("⚠️  Skipping PR creation — open PR already exists: %s", existingURL)
		return existingURL, false, nil
	}

	featureBranch := first.Naming.BranchName
	if err := createFeatureBranch(featureBranch); err != nil {
		return "", false, fmt.Errorf("failed to create feature branch %s: %w", featureBranch, err)
	}
	log.Printf("Created feature branch %s from %s\n", featureBranch, utils.OnboardBranch)

	cleanup := func(wrapped error) (string, bool, error) {
		if delErr := deleteRemoteBranch(featureBranch); delErr != nil {
			log.Printf("⚠️  Failed to clean up remote branch %s: %v\n", featureBranch, delErr)
		} else {
			log.Printf("Cleaned up remote branch %s after failure\n", featureBranch)
		}
		return "", false, wrapped
	}

	componentNames, files, err := collectFiles(entry.Components)
	if err != nil {
		return cleanup(err)
	}

	commitMessage := fmt.Sprintf("[Dalec] Add specs for %s", strings.Join(componentNames, ", "))
	if err := commitAllFiles(featureBranch, commitMessage, files); err != nil {
		return cleanup(fmt.Errorf("failed to commit files: %w", err))
	}
	log.Printf("Committed %d file(s) to %s\n", len(files), featureBranch)

	prTitle, prBody := buildPRDescription(entry, componentNames)
	prURL, prNumber, err := createPullRequest(prTitle, prBody, featureBranch)
	if err != nil {
		return cleanup(fmt.Errorf("failed to create PR: %w", err))
	}
	log.Printf("Created PR #%d: %s\n", prNumber, prURL)

	if reviewers := collectReviewers(entry.Components); len(reviewers) > 0 {
		if err := addReviewers(prNumber, reviewers); err != nil {
			log.Printf("⚠️  Failed to add reviewers to PR #%d: %v\n", prNumber, err)
		} else {
			log.Printf("Added %d reviewer(s) to PR #%d\n", len(reviewers), prNumber)
		}
	}

	return prURL, true, nil
}

// deriveFeatureBranch returns the branch name for a PR entry.
// Uses the pre-computed Naming.BranchName from the first component.
func deriveFeatureBranch(entry PREntry) string {
	return entry.Components[0].Naming.BranchName
}

// createFeatureBranch creates a new branch from the tip of OnboardBranch.
func createFeatureBranch(branchName string) error {
	refPath := fmt.Sprintf("repos/%s/%s/git/ref/heads/%s", utils.OnboardOwner, utils.OnboardRepo, utils.OnboardBranch)
	ref, err := repository.FetchJSON(refPath)
	if err != nil {
		return fmt.Errorf("failed to get ref for %s: %w", utils.OnboardBranch, err)
	}
	refObject, _ := ref["object"].(map[string]interface{})
	sha, _ := refObject["sha"].(string)
	if sha == "" {
		return fmt.Errorf("could not resolve SHA for %s", utils.OnboardBranch)
	}

	createPath := fmt.Sprintf("repos/%s/%s/git/refs", utils.OnboardOwner, utils.OnboardRepo)
	_, err = repository.WriteJSON(createPath, repo.POST, map[string]interface{}{
		"ref": "refs/heads/" + branchName,
		"sha": sha,
	})
	if err != nil {
		return fmt.Errorf("failed to create branch %s: %w", branchName, err)
	}
	return nil
}

// fileEntry holds a file path, its content, and its Git file mode for batch committing.
type fileEntry struct {
	Path    string
	Content []byte
}

// collectFiles gathers all files for every component into a flat list.
// Returns the component names and the files to commit.
func collectFiles(components []ComponentSpec) ([]string, []fileEntry, error) {
	var names []string
	var files []fileEntry

	for _, comp := range components {
		names = append(names, comp.Onboard.SpecImageName)

		files = append(files, fileEntry{
			Path:    comp.Naming.SpecFilePath,
			Content: comp.SpecContent,
		})

		dir := comp.Onboard.SpecDir()
		files = append(files, collectSiblingFiles(comp, dir)...)
	}
	return names, files, nil
}

// collectSiblingFiles returns Dockerfile and Makefile entries when not in spec-only mode.
func collectSiblingFiles(comp ComponentSpec, dir string) []fileEntry {
	if comp.SpecOnly {
		return nil
	}

	var files []fileEntry
	if len(comp.Onboard.DockerfileContent) > 0 {
		files = append(files, fileEntry{
			Path:    fmt.Sprintf("%s/Dockerfile", dir),
			Content: comp.Onboard.DockerfileContent,
		})
	}
	if len(comp.Onboard.MakefileContent) > 0 {
		files = append(files, fileEntry{
			Path:    fmt.Sprintf("%s/Makefile", dir),
			Content: comp.Onboard.MakefileContent,
		})
	}
	return files
}

// commitAllFiles creates a single commit containing all files on the given branch
// using the Git Data API (blobs → tree → commit → ref update).
func commitAllFiles(branch, message string, files []fileEntry) error {
	repoPath := fmt.Sprintf("repos/%s/%s", utils.OnboardOwner, utils.OnboardRepo)

	// Get the current commit SHA of the branch.
	refResp, err := repository.FetchJSON(fmt.Sprintf("%s/git/ref/heads/%s", repoPath, branch))
	if err != nil {
		return fmt.Errorf("failed to get branch ref: %w", err)
	}
	refObject, _ := refResp["object"].(map[string]interface{})
	parentSHA, _ := refObject["sha"].(string)

	// Get the base tree SHA from the parent commit.
	commitResp, err := repository.FetchJSON(fmt.Sprintf("%s/git/commits/%s", repoPath, parentSHA))
	if err != nil {
		return fmt.Errorf("failed to get parent commit: %w", err)
	}
	treeObj, _ := commitResp["tree"].(map[string]interface{})
	baseTreeSHA, _ := treeObj["sha"].(string)

	// Create a blob for each file and build tree entries.
	var treeEntries []map[string]interface{}
	for _, file := range files {
		blobSHA, err := createBlob(repoPath, file.Content)
		if err != nil {
			return fmt.Errorf("failed to create blob for %s: %w", file.Path, err)
		}
		treeEntries = append(treeEntries, map[string]interface{}{
			"path": file.Path,
			"mode": "100755",
			"type": "blob",
			"sha":  blobSHA,
		})
	}

	treeSHA, err := createTree(repoPath, baseTreeSHA, treeEntries)
	if err != nil {
		return fmt.Errorf("failed to create tree: %w", err)
	}

	commitSHA, err := createCommit(repoPath, message, treeSHA, parentSHA)
	if err != nil {
		return fmt.Errorf("failed to create commit: %w", err)
	}

	if err := updateBranchRef(repoPath, branch, commitSHA); err != nil {
		return fmt.Errorf("failed to update branch ref: %w", err)
	}

	return nil
}

// createBlob creates a blob in the repository and returns its SHA.
func createBlob(repoPath string, content []byte) (string, error) {
	resp, err := repository.WriteJSON(fmt.Sprintf("%s/git/blobs", repoPath), repo.POST, map[string]interface{}{
		"content":  base64.StdEncoding.EncodeToString(content),
		"encoding": "base64",
	})
	if err != nil {
		return "", err
	}
	sha, _ := resp["sha"].(string)
	return sha, nil
}

// createTree creates a new tree with the given entries on top of a base tree.
func createTree(repoPath, baseTreeSHA string, entries []map[string]interface{}) (string, error) {
	resp, err := repository.WriteJSON(fmt.Sprintf("%s/git/trees", repoPath), repo.POST, map[string]interface{}{
		"base_tree": baseTreeSHA,
		"tree":      entries,
	})
	if err != nil {
		return "", err
	}
	sha, _ := resp["sha"].(string)
	return sha, nil
}

// createCommit creates a commit with the given tree and parent.
func createCommit(repoPath, message, treeSHA, parentSHA string) (string, error) {
	resp, err := repository.WriteJSON(fmt.Sprintf("%s/git/commits", repoPath), repo.POST, map[string]interface{}{
		"message": message,
		"tree":    treeSHA,
		"parents": []string{parentSHA},
		"committer": map[string]string{
			"name":  "dalec-spec-generator",
			"email": "dalec-bot@microsoft.com",
		},
	})
	if err != nil {
		return "", err
	}
	sha, _ := resp["sha"].(string)
	return sha, nil
}

// updateBranchRef fast-forwards the branch to point at the given commit SHA.
func updateBranchRef(repoPath, branch, commitSHA string) error {
	_, err := repository.WriteJSON(
		fmt.Sprintf("%s/git/refs/heads/%s", repoPath, branch),
		repo.PATCH,
		map[string]interface{}{"sha": commitSHA},
	)
	return err
}

// buildPRDescription returns the title and body for the pull request,
// adapting for single vs multi-component entries.
func buildPRDescription(entry PREntry, componentNames []string) (title, body string) {
	first := entry.Components[0]
	n := first.Naming

	title = n.PRTitle
	if len(entry.Components) == 1 {
		body = fmt.Sprintf("Auto-generated Dalec spec for **%s** @ `%s`.\n\nRepository: %s\n\nRequires 1 reviewer approval before merge.",
			n.DisplayName, n.VersionRevision, first.Onboard.Repository)
	} else {
		body = fmt.Sprintf("Auto-generated Dalec specs for group **%s** @ `%s`.\n\nComponents: %s\n\nRequires 1 reviewer approval before merge.",
			n.DisplayName, n.VersionRevision, strings.Join(componentNames, ", "))
	}
	return title, body
}

// createPullRequest opens a PR from head into OnboardBranch.
// Returns the PR URL and PR number.
func createPullRequest(title, body, head string) (string, int, error) {
	prPath := fmt.Sprintf("repos/%s/%s/pulls", utils.OnboardOwner, utils.OnboardRepo)

	result, err := repository.WriteJSON(prPath, repo.POST, map[string]interface{}{
		"title": title,
		"body":  body,
		"head":  head,
		"base":  utils.OnboardBranch,
	})
	if err != nil {
		return "", 0, err
	}

	prURL, _ := result["html_url"].(string)
	prNumberFloat, _ := result["number"].(float64)
	prNumber := int(prNumberFloat)

	labelPath := fmt.Sprintf("repos/%s/%s/issues/%d/labels", utils.OnboardOwner, utils.OnboardRepo, prNumber)
	_, err = repository.WriteJSON(labelPath, repo.POST, map[string]interface{}{
		"labels": []string{"specfile"},
	})
	if err != nil {
		log.Printf("⚠️  Failed to add 'specfile' label to PR #%d: %v", prNumber, err)
	}

	return prURL, prNumber, nil
}

// collectReviewers returns a deduplicated list of reviewers across all components.
func collectReviewers(components []ComponentSpec) []string {
	seen := make(map[string]bool)
	var reviewers []string
	for _, comp := range components {
		for _, reviewer := range comp.Onboard.Reviewers {
			if seen[reviewer] {
				continue
			}
			seen[reviewer] = true
			reviewers = append(reviewers, reviewer)
		}
	}
	return reviewers
}

// addReviewers requests reviews from the given GitHub usernames or email addresses.
func addReviewers(prNumber int, reviewers []string) error {
	reviewPath := fmt.Sprintf("repos/%s/%s/pulls/%d/requested_reviewers", utils.OnboardOwner, utils.OnboardRepo, prNumber)

	_, err := repository.WriteJSON(reviewPath, repo.POST, map[string]interface{}{
		"reviewers": reviewers,
	})
	return err
}

// findExistingPR searches for an open PR with the "specfile" label whose title
// matches the given component display name and version-revision.
// Returns the PR URL if found, or "" if no matching PR exists.
func findExistingPR(displayName, versionRevision string) (string, error) {
	issuesPath := fmt.Sprintf("repos/%s/%s/issues?state=open&labels=specfile&per_page=100",
		utils.OnboardOwner, utils.OnboardRepo)
	issues, err := repository.FetchJSONArray(issuesPath)
	if err != nil {
		return "", fmt.Errorf("failed to list open specfile PRs: %w", err)
	}

	titleSuffix := displayName + " @ " + versionRevision
	for _, issue := range issues {
		if issue["pull_request"] == nil {
			continue
		}
		title, _ := issue["title"].(string)
		if !strings.HasSuffix(title, titleSuffix) {
			continue
		}
		htmlURL, _ := issue["html_url"].(string)
		return htmlURL, nil
	}
	return "", nil
}

// deleteRemoteBranch deletes a branch from the onboard repo via the GitHub API.
func deleteRemoteBranch(branchName string) error {
	refPath := fmt.Sprintf("repos/%s/%s/git/refs/heads/%s", utils.OnboardOwner, utils.OnboardRepo, branchName)
	_, err := repository.WriteJSON(refPath, repo.DELETE, nil)
	if err != nil {
		return fmt.Errorf("failed to delete remote branch %s: %w", branchName, err)
	}
	return nil
}
