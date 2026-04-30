// ═══════════════════════════════════════════════════════════════════════════════
// Step 8 — Create Pull Request
//
//   Creates a feature branch from OnboardBranch, commits specfiles,
//   Dockerfiles, and Makefiles for one or more components, then opens
//   a single PR merging the feature branch into OnboardBranch.
//   Reviewers from the onboarding configs are added to the PR.
//
//   Chunk 1 · MAIN      CreatePR()
//   Chunk 2 · STEPS     deriveFeatureBranch(), commitComponentFiles(),
//                        buildPRDescription(), collectReviewers()
//   Chunk 3 · GIT       createFeatureBranch(), commitFileToBranch(),
//                        createPullRequest(), addReviewers(),
//                        deleteRemoteBranch()
// ═══════════════════════════════════════════════════════════════════════════════

package workflow

import (
	"encoding/base64"
	"fmt"
	"log"
	"strings"

	"dalec-mapping/domain/onboarding"
	repo "dalec-mapping/domain/repository"
	"dalec-mapping/infrastructure/repository"
	"dalec-mapping/utils"
)

// ─── Chunk 1 · MAIN ─────────────────────────────────────────────────────────

// ComponentSpec holds one component's contribution to a PR.
type ComponentSpec struct {
	Onboard     *onboarding.ComponentConfig
	Tag         string
	SpecContent []byte // generated spec file content
	SpecOnly    bool
	RemotePath  string // remote spec file path for downstream consumption
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
// Returns the PR URL on success.
func CreatePR(entry PREntry) (string, error) {
	if len(entry.Components) == 0 {
		return "", fmt.Errorf("no components for %s", entry.GroupName)
	}

	// 0. Derive and create the feature branch.
	featureBranch := deriveFeatureBranch(entry)
	if err := createFeatureBranch(featureBranch); err != nil {
		return "", fmt.Errorf("failed to create feature branch %s: %w", featureBranch, err)
	}
	log.Printf("🌿 Created feature branch %s from %s\n", featureBranch, utils.OnboardBranch)

	cleanup := func(wrapped error) (string, error) {
		if delErr := deleteRemoteBranch(featureBranch); delErr != nil {
			log.Printf("⚠️  Failed to clean up remote branch %s: %v\n", featureBranch, delErr)
		} else {
			log.Printf("🧹 Cleaned up remote branch %s after failure\n", featureBranch)
		}
		return "", wrapped
	}

	// 1. Commit each component's files to the shared branch.
	componentNames, err := commitComponentFiles(entry.Components, featureBranch)
	if err != nil {
		return cleanup(err)
	}

	// 2. Create the pull request.
	prTitle, prBody := buildPRDescription(entry, componentNames)
	prURL, prNumber, err := createPullRequest(prTitle, prBody, featureBranch)
	if err != nil {
		return cleanup(fmt.Errorf("failed to create PR: %w", err))
	}
	log.Printf("📝 Created PR #%d: %s\n", prNumber, prURL)

	// 3. Add reviewers.
	if reviewers := collectReviewers(entry.Components); len(reviewers) > 0 {
		if err := addReviewers(prNumber, reviewers); err != nil {
			log.Printf("⚠️  Failed to add reviewers to PR #%d: %v\n", prNumber, err)
		} else {
			log.Printf("👥 Added %d reviewer(s) to PR #%d\n", len(reviewers), prNumber)
		}
	}

	return prURL, nil
}

// ─── Chunk 2 · STEPS ─────────────────────────────────────────────────────────

// deriveFeatureBranch returns the branch name for a PR entry.
// Standalone: dalec/<leaf>-<tag>, grouped: dalec/<repo>/<group>-<tag>.
func deriveFeatureBranch(entry PREntry) string {
	first := entry.Components[0]
	safeTag := strings.ReplaceAll(first.Tag, "/", "-")

	if len(entry.Components) == 1 && entry.GroupName == "" {
		return fmt.Sprintf("dalec/%s-%s", first.Onboard.SpecLeaf(), safeTag)
	}
	return fmt.Sprintf("dalec/%s/%s-%s", first.Onboard.SpecRepository, entry.GroupName, safeTag)
}

// commitComponentFiles commits each component's specfile, Dockerfile, and
// Makefile to the given branch. Returns the list of component names committed.
func commitComponentFiles(components []ComponentSpec, branch string) ([]string, error) {
	var names []string
	for _, comp := range components {
		onboard := comp.Onboard
		dir := onboard.SpecDir()
		specImageName := onboard.SpecImageName
		names = append(names, specImageName)

		specFile := fmt.Sprintf("%s-%s-specfile.yml", specImageName, comp.Tag)
		if err := commitFileToBranch(
			fmt.Sprintf("%s/%s", dir, specFile),
			fmt.Sprintf("Add %s", specFile),
			comp.SpecContent,
			branch,
		); err != nil {
			return nil, fmt.Errorf("failed to commit spec file for %s: %w", specImageName, err)
		}

		if !comp.SpecOnly && len(onboard.DockerfileContent) > 0 {
			if err := commitFileToBranch(
				fmt.Sprintf("%s/Dockerfile", dir),
				fmt.Sprintf("Add Dockerfile for %s", specImageName),
				onboard.DockerfileContent,
				branch,
			); err != nil {
				return nil, fmt.Errorf("failed to commit Dockerfile for %s: %w", specImageName, err)
			}
		}

		if !comp.SpecOnly && len(onboard.MakefileContent) > 0 {
			if err := commitFileToBranch(
				fmt.Sprintf("%s/Makefile", dir),
				fmt.Sprintf("Add Makefile for %s", specImageName),
				onboard.MakefileContent,
				branch,
			); err != nil {
				return nil, fmt.Errorf("failed to commit Makefile for %s: %w", specImageName, err)
			}
		}

		// Copy tests/ directory from OnboardBranch if it exists.
		if err := commitTestFiles(dir, specImageName, branch); err != nil {
			return nil, fmt.Errorf("failed to commit test files for %s: %w", specImageName, err)
		}
	}
	return names, nil
}

// buildPRDescription returns the title and body for the pull request,
// adapting for single vs multi-component entries.
func buildPRDescription(entry PREntry, componentNames []string) (title, body string) {
	first := entry.Components[0]
	if len(entry.Components) == 1 {
		onboard := first.Onboard
		title = fmt.Sprintf("[Dalec] %s @ %s", onboard.SpecImageName, first.Tag)
		body = fmt.Sprintf("Auto-generated Dalec spec for **%s** @ `%s`.\n\nRepository: %s\n\nRequires 1 reviewer approval before merge.",
			onboard.SpecImageName, first.Tag, onboard.Repository)
	} else {
		title = fmt.Sprintf("[Dalec] %s @ %s", entry.GroupName, first.Tag)
		body = fmt.Sprintf("Auto-generated Dalec specs for group **%s** @ `%s`.\n\nComponents: %s\n\nRequires 1 reviewer approval before merge.",
			entry.GroupName, first.Tag, strings.Join(componentNames, ", "))
	}
	return title, body
}

// collectReviewers returns a deduplicated list of reviewers across all components.
func collectReviewers(components []ComponentSpec) []string {
	seen := make(map[string]bool)
	var reviewers []string
	for _, comp := range components {
		for _, r := range comp.Onboard.Reviewers {
			if !seen[r] {
				seen[r] = true
				reviewers = append(reviewers, r)
			}
		}
	}
	return reviewers
}

// commitTestFiles fetches all files from the tests/ directory under a
// component's SpecDir on OnboardBranch and commits them to the feature branch.
// If no tests/ directory exists, it silently returns nil.
func commitTestFiles(dir, specImageName, branch string) error {
	testsDir := dir + "/tests"
	contentsPath := fmt.Sprintf("repos/%s/%s/contents/%s?ref=%s",
		utils.OnboardOwner, utils.OnboardRepo, testsDir, utils.OnboardBranch)

	items, err := repository.FetchJSONArray(contentsPath)
	if err != nil {
		// No tests directory — not an error
		return nil
	}

	for _, item := range items {
		itemType, _ := item["type"].(string)
		name, _ := item["name"].(string)
		downloadURL, _ := item["download_url"].(string)
		if name == "" || downloadURL == "" {
			continue
		}
		if itemType == "dir" {
			// Only handle top-level test files; skip subdirectories
			continue
		}

		content, err := repository.FetchRawContent(downloadURL)
		if err != nil {
			return fmt.Errorf("failed to fetch test file %s: %w", name, err)
		}
		remotePath := fmt.Sprintf("%s/tests/%s", dir, name)
		if err := commitFileToBranch(
			remotePath,
			fmt.Sprintf("Add test file %s for %s", name, specImageName),
			content,
			branch,
		); err != nil {
			return fmt.Errorf("failed to commit test file %s: %w", name, err)
		}
	}
	return nil
}

// ─── Chunk 3 · GIT ───────────────────────────────────────────────────────────

// commitFileToBranch pushes a single file to a specific branch via the GitHub
// Contents API. If the file already exists on that branch, its SHA is included
// to perform an update.
func commitFileToBranch(filePath, message string, content []byte, branch string) error {
	encoded := base64.StdEncoding.EncodeToString(content)
	contentsPath := fmt.Sprintf("repos/%s/%s/contents/%s", utils.OnboardOwner, utils.OnboardRepo, filePath)
	putPayload := map[string]interface{}{
		"message": message,
		"committer": map[string]string{
			"name":  "dalec-spec-generator",
			"email": "dalec-bot@microsoft.com",
		},
		"content": encoded,
		"branch":  branch,
	}

	// Include SHA for updates to existing files on the branch
	existingFile, err := repository.FetchJSON(fmt.Sprintf("%s?ref=%s", contentsPath, branch))
	if err == nil {
		if sha, ok := existingFile["sha"].(string); ok {
			putPayload["sha"] = sha
		}
	}

	_, err = repository.WriteJSON(contentsPath, repo.PUT, putPayload)
	if err != nil {
		return fmt.Errorf("failed to commit %s to branch %s: %w", filePath, branch, err)
	}
	log.Printf("  📄 Committed %s to %s\n", filePath, branch)
	return err
}

// createFeatureBranch creates a new branch from the tip of OnboardBranch.
func createFeatureBranch(branchName string) error {
	// Get the SHA of OnboardBranch
	refPath := fmt.Sprintf("repos/%s/%s/git/ref/heads/%s", utils.OnboardOwner, utils.OnboardRepo, utils.OnboardBranch)
	ref, err := repository.FetchJSON(refPath)
	if err != nil {
		return fmt.Errorf("failed to get ref for %s: %w", utils.OnboardBranch, err)
	}
	obj, _ := ref["object"].(map[string]interface{})
	sha, _ := obj["sha"].(string)
	if sha == "" {
		return fmt.Errorf("could not resolve SHA for %s", utils.OnboardBranch)
	}

	// Create the new branch ref
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

	// Add "specfile" label to the PR.
	labelPath := fmt.Sprintf("repos/%s/%s/issues/%d/labels", utils.OnboardOwner, utils.OnboardRepo, prNumber)
	_, err = repository.WriteJSON(labelPath, repo.POST, map[string]interface{}{
		"labels": []string{"specfile"},
	})
	if err != nil {
		log.Printf("⚠️  Failed to add 'specfile' label to PR #%d: %v", prNumber, err)
	}

	return prURL, prNumber, nil
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

// addReviewers requests reviews from the given GitHub usernames or email addresses.
// GitHub's API expects usernames; email addresses are mapped to usernames where possible.
func addReviewers(prNumber int, reviewers []string) error {
	reviewPath := fmt.Sprintf("repos/%s/%s/pulls/%d/requested_reviewers", utils.OnboardOwner, utils.OnboardRepo, prNumber)

	_, err := repository.WriteJSON(reviewPath, repo.POST, map[string]interface{}{
		"reviewers": reviewers,
	})
	return err
}
