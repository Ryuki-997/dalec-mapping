// ═══════════════════════════════════════════════════════════════════════════════
// PR — Create Pull Request
//
//   Creates a feature branch from OnboardBranch, commits specfiles,
//   Dockerfiles, and Makefiles for one or more publishable WorkComponents,
//   then opens a single PR merging the feature branch into OnboardBranch.
//
//   Functions are ordered by call sequence:
//     CreatePR()
//       → findExistingPR()
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
// ═══════════════════════════════════════════════════════════════════════════════

package specrepo

import (
	"encoding/base64"
	"fmt"
	"log"
	"strings"

	"dalec-mapping/config"
	"dalec-mapping/domain/buildresult"
	"dalec-mapping/domain/naming"
	"dalec-mapping/domain/pathcache"
	repo "dalec-mapping/domain/repository"
	"dalec-mapping/domain/workplan"
	"dalec-mapping/workflow/infrastructure/github"
)

// ForcePR skips the existing-PR check in CreatePR when true.
// Set by the CLI flag parser in workflow/phases.
var ForcePR bool

// CreatePR creates a feature branch from OnboardBranch, commits all publishable
// components' files to it, and opens a single PR. Works for both standalone
// components (one component, one component) and grouped components (multiple
// components, each with one or more components). The group carries the Phase 1
// metadata (GroupName, PRID); publishable components are collected by walking
// every component's Components and filtering by Result.IsPublishable() inside
// this call. BranchName + PRTitle are read from the first publishable
// component's Naming — they were baked in by Phase 1. BuildFiles snapshot paths
// whose remote location already exists in pathcache.Cache are skipped
// (not re-committed).
//
// Returns:
//   - prURL: the PR URL, or "" when the group has no publishable components
//   - created: true when a new PR was opened, false when an existing PR was reused
//   - specPaths: the spec paths committed in this PR, in publishable order
//   - err: non-nil only on a hard failure (branch/commit/PR creation)
func CreatePR(group workplan.WorkGroup) (string, bool, []string, error) {
	publishable := collectPublishableComponents(group)
	if len(publishable) == 0 {
		return "", false, nil, nil
	}

	firstNaming := publishable[0].Naming

	// Check for an existing open PR before creating a new one.
	if !ForcePR {
		existingURL, err := findExistingPR(firstNaming)
		if err != nil {
			log.Printf("⚠️  Failed to check for existing PRs, proceeding with creation: %v", err)
		} else if existingURL != "" {
			log.Printf("⚠️  Skipping PR creation — open PR already exists: %s", existingURL)
			return existingURL, false, collectSpecPaths(publishable), nil
		}
	}

	featureBranch := firstNaming.BranchName
	if err := createFeatureBranch(featureBranch); err != nil {
		return "", false, nil, fmt.Errorf("failed to create feature branch %s: %w", featureBranch, err)
	}
	log.Printf("Created feature branch %s from %s\n", featureBranch, config.OnboardBranch)

	specImageNames, files := collectFiles(publishable)

	commitMessage := fmt.Sprintf("[Dalec] Add specs for %s", strings.Join(specImageNames, ", "))
	if err := commitAllFiles(featureBranch, commitMessage, files); err != nil {
		return "", false, nil, fmt.Errorf("failed to commit files: %w", err)
	}
	log.Printf("Committed %d file(s) to %s\n", len(files), featureBranch)

	prTitle, prBody := buildPRDescription(publishable, specImageNames)
	prURL, prNumber, err := createPullRequest(prTitle, prBody, featureBranch)
	if err != nil {
		return "", false, nil, fmt.Errorf("failed to create PR: %w", err)
	}
	log.Printf("Created PR #%d: %s\n", prNumber, prURL)

	return prURL, true, collectSpecPaths(publishable), nil
}

// collectPublishableComponents walks the flat group.Components list and returns the
// subset of components whose Result.IsPublishable() is true, preserving order.
func collectPublishableComponents(group workplan.WorkGroup) []*workplan.WorkComponent {
	var publishable []*workplan.WorkComponent
	for _, component := range group.Components {
		if !component.Result.IsPublishable() {
			continue
		}
		publishable = append(publishable, component)
	}
	return publishable
}

// collectSpecPaths returns each publishable component's spec file path, used by
// callers to populate the publish outcome's spec listing.
func collectSpecPaths(publishable []*workplan.WorkComponent) []string {
	specPaths := make([]string, 0, len(publishable))
	for _, component := range publishable {
		specPaths = append(specPaths, component.Naming.SpecFilePath)
	}
	return specPaths
}

// createFeatureBranch creates a new branch from the tip of OnboardBranch.
func createFeatureBranch(branchName string) error {
	ref, err := github.FetchJSON(pathcache.OnboardAPIPath("git/ref/heads/%s", config.OnboardBranch))
	if err != nil {
		return fmt.Errorf("failed to get ref for %s: %w", config.OnboardBranch, err)
	}
	refObject, _ := ref["object"].(map[string]interface{})
	sha, _ := refObject["sha"].(string)
	if sha == "" {
		return fmt.Errorf("could not resolve SHA for %s", config.OnboardBranch)
	}

	_, err = github.WriteJSON(pathcache.OnboardAPIPath("git/refs"), repo.POST, map[string]interface{}{
		"ref": "refs/heads/" + branchName,
		"sha": sha,
	})
	if err != nil {
		return fmt.Errorf("failed to create branch %s: %w", branchName, err)
	}
	return nil
}

// fileEntry holds a file path and its content for batch committing.
type fileEntry struct {
	Path    string
	Content []byte
}

// collectFiles gathers all files for every publishable component into a flat list.
// Returns the spec image names and the files to commit.
func collectFiles(publishable []*workplan.WorkComponent) ([]string, []fileEntry) {
	var names []string
	var files []fileEntry

	for _, component := range publishable {
		names = append(names, component.Naming.SpecImageName)

		files = append(files, fileEntry{
			Path:    component.Naming.SpecFilePath,
			Content: component.Result.SpecContent,
		})

		files = append(files, collectSiblingFiles(component)...)
	}
	return names, files
}

// collectSiblingFiles returns per-version BuildFiles snapshot entries for the
// component. Skipped for revision bumps (same version → snapshot unchanged),
// when the work component has no in-memory build-file sources, and on a per-file
// basis when the snapshot path already exists in pathcache.Cache.
func collectSiblingFiles(component *workplan.WorkComponent) []fileEntry {
	if component.Result.Outcome == buildresult.OutcomeBumpRevision {
		return nil
	}

	buildFiles := component.BuildFiles
	versionRevision := component.Naming.VersionRevision

	var files []fileEntry
	if len(buildFiles.Dockerfile.Source) > 0 {
		path := pathcache.BuildDockerfilePath(component.Naming, versionRevision)
		if pathcache.Has(path) {
			log.Printf("⚠️  Skipping BuildFiles snapshot — already exists: %s", path)
		} else {
			files = append(files, fileEntry{Path: path, Content: buildFiles.Dockerfile.Source})
		}
	}
	if len(buildFiles.Makefile.Source) > 0 {
		path := pathcache.BuildMakefilePath(component.Naming, versionRevision)
		if pathcache.Has(path) {
			log.Printf("⚠️  Skipping BuildFiles snapshot — already exists: %s", path)
		} else {
			files = append(files, fileEntry{Path: path, Content: buildFiles.Makefile.Source})
		}
	}
	return files
}

// commitAllFiles creates a single commit containing all files on the given branch
// using the Git Data API (blobs → tree → commit → ref update).
func commitAllFiles(branch, message string, files []fileEntry) error {
	// Get the current commit SHA of the branch.
	refResp, err := github.FetchJSON(pathcache.OnboardAPIPath("git/ref/heads/%s", branch))
	if err != nil {
		return fmt.Errorf("failed to get branch ref: %w", err)
	}
	refObject, _ := refResp["object"].(map[string]interface{})
	parentSHA, _ := refObject["sha"].(string)

	// Get the base tree SHA from the parent commit.
	commitResp, err := github.FetchJSON(pathcache.OnboardAPIPath("git/commits/%s", parentSHA))
	if err != nil {
		return fmt.Errorf("failed to get parent commit: %w", err)
	}
	treeObj, _ := commitResp["tree"].(map[string]interface{})
	baseTreeSHA, _ := treeObj["sha"].(string)

	// Create a blob for each file and build tree entries.
	var treeEntries []map[string]interface{}
	for _, file := range files {
		blobSHA, err := createBlob(file.Content)
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

	treeSHA, err := createTree(baseTreeSHA, treeEntries)
	if err != nil {
		return fmt.Errorf("failed to create tree: %w", err)
	}

	commitSHA, err := createCommit(message, treeSHA, parentSHA)
	if err != nil {
		return fmt.Errorf("failed to create commit: %w", err)
	}

	if err := updateBranchRef(branch, commitSHA); err != nil {
		return fmt.Errorf("failed to update branch ref: %w", err)
	}

	return nil
}

// createBlob creates a blob in the spec repository and returns its SHA.
func createBlob(content []byte) (string, error) {
	resp, err := github.WriteJSON(pathcache.OnboardAPIPath("git/blobs"), repo.POST, map[string]interface{}{
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
func createTree(baseTreeSHA string, entries []map[string]interface{}) (string, error) {
	resp, err := github.WriteJSON(pathcache.OnboardAPIPath("git/trees"), repo.POST, map[string]interface{}{
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
func createCommit(message, treeSHA, parentSHA string) (string, error) {
	resp, err := github.WriteJSON(pathcache.OnboardAPIPath("git/commits"), repo.POST, map[string]interface{}{
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
func updateBranchRef(branch, commitSHA string) error {
	_, err := github.WriteJSON(
		pathcache.OnboardAPIPath("git/refs/heads/%s", branch),
		repo.PATCH,
		map[string]interface{}{"sha": commitSHA},
	)
	return err
}

// buildPRDescription returns the title and body for the pull request,
// adapting for single vs multi-component groups. Reads the partner repository
// URL from the first publishable component's denormalized Repository field.
func buildPRDescription(publishable []*workplan.WorkComponent, specImageNames []string) (title, body string) {
	n := publishable[0].Naming

	title = n.PRTitle
	if len(publishable) == 1 {
		body = fmt.Sprintf("Auto-generated Dalec spec for **%s** @ `%s`.\n\nRepository: %s\n\nRequires 1 reviewer approval before merge.",
			n.DisplayName, n.VersionRevision, publishable[0].ParentGroup.Repository)
		return title, body
	}
	body = fmt.Sprintf("Auto-generated Dalec specs for group **%s** @ `%s`.\n\nComponents: %s\n\nRequires 1 reviewer approval before merge.",
		n.DisplayName, n.VersionRevision, strings.Join(specImageNames, ", "))
	return title, body
}

// createPullRequest opens a PR from head into OnboardBranch.
// Returns the PR URL and PR number.
func createPullRequest(title, body, head string) (string, int, error) {
	result, err := github.WriteJSON(pathcache.OnboardAPIPath("pulls"), repo.POST, map[string]interface{}{
		"title": title,
		"body":  body,
		"head":  head,
		"base":  config.OnboardBranch,
	})
	if err != nil {
		return "", 0, err
	}

	prURL, _ := result["html_url"].(string)
	prNumberFloat, _ := result["number"].(float64)
	prNumber := int(prNumberFloat)

	_, err = github.WriteJSON(pathcache.OnboardAPIPath("issues/%d/labels", prNumber), repo.POST, map[string]interface{}{
		"labels": []string{"specfile"},
	})
	if err != nil {
		log.Printf("⚠️  Failed to add 'specfile' label to PR #%d: %v", prNumber, err)
	}

	return prURL, prNumber, nil
}

// findExistingPR searches for an open PR with the "specfile" label whose title
// matches the given component display name and version-revision.
// Returns the PR URL if found, or "" if no matching PR exists.
func findExistingPR(n naming.Naming) (string, error) {
	issues, err := github.FetchJSONArray(pathcache.OnboardAPIPath("issues?state=open&labels=specfile&per_page=100"))
	if err != nil {
		return "", fmt.Errorf("failed to list open specfile PRs: %w", err)
	}

	titleSuffix := n.DisplayName + " @ " + n.VersionRevision
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
