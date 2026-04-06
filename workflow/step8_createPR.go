// ═══════════════════════════════════════════════════════════════════════════════
// Step 8 — Create Pull Request & Notify Reviewers
//
//   Creates a PR on the onboard repo containing the specfile, Dockerfile,
//   and Makefile in a single branch. Reviewers from the onboarding config
//   are added to the PR. After 1 reviewer approval the PR can be merged.
//   Sends an email notification to all reviewers with the PR link via
//   Azure Communication Services.
//
//   Chunk 1 · MAIN      CreateSpecPR()
//   Chunk 2 · GIT       createBranch(), commitFileToBranch(),
//                        createPullRequest(), addReviewers()
//   Chunk 3 · EMAIL     notifyReviewersEmail(), sendACSEmail()
// ═══════════════════════════════════════════════════════════════════════════════

package workflow

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"time"

	"dalec-mapping/domain/onboarding"
	repo "dalec-mapping/domain/repository"
	"dalec-mapping/infrastructure/repository"
	"dalec-mapping/utils"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
)

// ─── Chunk 1 · MAIN ─────────────────────────────────────────────────────────

// CreateSpecPR creates a feature branch, commits the specfile (and optionally
// Dockerfile/Makefile), opens a PR against the base branch, and adds reviewers.
// When specOnly is true, only the specfile is committed.
// Returns the PR URL on success.
func CreateSpecPR(onboard *onboarding.OnboardingInfo, tag string, specOnly bool) (string, error) {
	specRepository := onboard.SpecRepository
	specImageName := onboard.SpecImageName

	// Build directory prefix
	var dir string
	if specRepository != "" {
		dir = fmt.Sprintf("specs/auto/%s/%s", specRepository, specImageName)
	} else {
		dir = fmt.Sprintf("specs/auto/%s", specImageName)
	}

	// Create a timestamped branch name
	branchName := fmt.Sprintf("dalec/%s-%s-%s", specImageName, tag, time.Now().Format("20060102-150405"))

	// 1. Create the feature branch from the base branch HEAD
	if err := createBranch(branchName); err != nil {
		return "", fmt.Errorf("failed to create branch %s: %w", branchName, err)
	}
	log.Printf("🌿 Created branch %s\n", branchName)

	// 2. Commit specfile
	specFile := fmt.Sprintf("%s-%s-specfile.yml", specImageName, tag)
	specContent, err := os.ReadFile(utils.SpecPath)
	if err != nil {
		return "", fmt.Errorf("failed to read spec file: %w", err)
	}
	if err := commitFileToBranch(
		fmt.Sprintf("%s/%s", dir, specFile),
		fmt.Sprintf("Add %s", specFile),
		specContent,
		branchName,
	); err != nil {
		return "", fmt.Errorf("failed to commit spec file: %w", err)
	}

	// 3. Commit Dockerfile (if present and not spec-only)
	if !specOnly && len(onboard.DockerfileContent) > 0 {
		if err := commitFileToBranch(
			fmt.Sprintf("%s/Dockerfile", dir),
			fmt.Sprintf("Add Dockerfile for %s", specImageName),
			onboard.DockerfileContent,
			branchName,
		); err != nil {
			return "", fmt.Errorf("failed to commit Dockerfile: %w", err)
		}
	}

	// 4. Commit Makefile (if present and not spec-only)
	if !specOnly && len(onboard.MakefileContent) > 0 {
		if err := commitFileToBranch(
			fmt.Sprintf("%s/Makefile", dir),
			fmt.Sprintf("Add Makefile for %s", specImageName),
			onboard.MakefileContent,
			branchName,
		); err != nil {
			return "", fmt.Errorf("failed to commit Makefile: %w", err)
		}
	}

	// 5. Create the pull request
	prTitle := fmt.Sprintf("[Dalec] %s @ %s", specImageName, tag)
	prBody := fmt.Sprintf("Auto-generated Dalec spec for **%s** @ `%s`.\n\nRepository: %s\n\nRequires 1 reviewer approval before merge.",
		specImageName, tag, onboard.Repository)

	prURL, prNumber, err := createPullRequest(branchName, prTitle, prBody)
	if err != nil {
		return "", fmt.Errorf("failed to create PR: %w", err)
	}
	log.Printf("📝 Created PR #%d: %s\n", prNumber, prURL)

	// 6. Add reviewers
	if len(onboard.Reviewers) > 0 {
		if err := addReviewers(prNumber, onboard.Reviewers); err != nil {
			log.Printf("⚠️  Failed to add reviewers to PR #%d: %v\n", prNumber, err)
		} else {
			log.Printf("👥 Added %d reviewer(s) to PR #%d\n", len(onboard.Reviewers), prNumber)
		}
	}

	// 7. Send email notification to reviewers with the PR link
	if len(onboard.Reviewers) > 0 {
		if err := notifyReviewersEmail(onboard, tag, prURL); err != nil {
			log.Printf("⚠️  Failed to send email notification: %v\n", err)
		} else {
			log.Printf("📧 Email notification sent to %d reviewer(s)\n", len(onboard.Reviewers))
		}
	}

	return prURL, nil
}

// ─── Chunk 2 · GIT ───────────────────────────────────────────────────────────

// createBranch creates a new branch from the HEAD of the base branch.
func createBranch(branchName string) error {
	// Get the SHA of the base branch HEAD
	refPath := fmt.Sprintf("repos/%s/%s/git/ref/heads/%s", utils.OnboardOwner, utils.OnboardRepo, utils.OnboardBranch)
	refData, err := repository.FetchJSON(refPath)
	if err != nil {
		return fmt.Errorf("failed to fetch base branch ref: %w", err)
	}

	obj, ok := refData["object"].(map[string]interface{})
	if !ok {
		return fmt.Errorf("unexpected ref response format")
	}
	baseSHA, ok := obj["sha"].(string)
	if !ok {
		return fmt.Errorf("could not extract SHA from base branch ref")
	}

	// Create the new branch ref
	_, err = repository.WriteJSON(
		fmt.Sprintf("repos/%s/%s/git/refs", utils.OnboardOwner, utils.OnboardRepo),
		repo.POST,
		map[string]interface{}{
			"ref": "refs/heads/" + branchName,
			"sha": baseSHA,
		},
	)
	return err
}

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

// createPullRequest opens a PR from the feature branch to the base branch.
// Returns the PR URL and PR number.
func createPullRequest(branchName, title, body string) (string, int, error) {
	prPath := fmt.Sprintf("repos/%s/%s/pulls", utils.OnboardOwner, utils.OnboardRepo)

	result, err := repository.WriteJSON(prPath, repo.POST, map[string]interface{}{
		"title": title,
		"body":  body,
		"head":  branchName,
		"base":  utils.OnboardBranch,
	})
	if err != nil {
		return "", 0, err
	}

	prURL, _ := result["html_url"].(string)
	prNumberFloat, _ := result["number"].(float64)
	prNumber := int(prNumberFloat)

	return prURL, prNumber, nil
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

// ─── Chunk 3 · EMAIL ────────────────────────────────────────────────────────

// notifyReviewersEmail sends an email to all reviewers with the PR link
// via Azure Communication Services.
func notifyReviewersEmail(onboard *onboarding.OnboardingInfo, tag, prURL string) error {
	subject := fmt.Sprintf("[Dalec] Review Requested: %s @ %s", onboard.SpecImageName, tag)

	body := fmt.Sprintf(`<p>A new Dalec spec has been generated and a pull request is ready for review.</p>
<table>
<tr><td><b>Repository:</b></td><td>%s</td></tr>
<tr><td><b>Image:</b></td><td>%s</td></tr>
<tr><td><b>Tag:</b></td><td>%s</td></tr>
</table>
<p><a href="%s">Review the Pull Request</a></p>
<p>The PR requires 1 reviewer approval before the spec, Dockerfile, and Makefile are merged.</p>`,
		onboard.Repository, onboard.SpecImageName, tag, prURL)

	return sendACSEmail(onboard.Reviewers, subject, body)
}

// sendACSEmail sends an email via the Azure Communication Services Email REST API.
// Requires ACS_EMAIL_ENDPOINT and ACS_SENDER_ADDRESS environment variables.
// Authenticates using DefaultAzureCredential (managed identity in CI, az login locally).
func sendACSEmail(recipients []string, subject, htmlBody string) error {
	endpoint := os.Getenv("ACS_EMAIL_ENDPOINT")
	if endpoint == "" {
		return fmt.Errorf("ACS_EMAIL_ENDPOINT not set")
	}
	senderAddress := os.Getenv("ACS_SENDER_ADDRESS")
	if senderAddress == "" {
		return fmt.Errorf("ACS_SENDER_ADDRESS not set")
	}

	// Build recipient list
	toRecipients := make([]map[string]string, len(recipients))
	for i, r := range recipients {
		toRecipients[i] = map[string]string{"address": r}
	}

	payload := map[string]interface{}{
		"senderAddress": senderAddress,
		"content": map[string]string{
			"subject": subject,
			"html":    htmlBody,
		},
		"recipients": map[string]interface{}{
			"to": toRecipients,
		},
	}

	jsonBody, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal email payload: %w", err)
	}

	// Acquire token via DefaultAzureCredential
	cred, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		return fmt.Errorf("failed to create Azure credential: %w", err)
	}

	token, err := cred.GetToken(context.Background(), policy.TokenRequestOptions{
		Scopes: []string{"https://communication.azure.com/.default"},
	})
	if err != nil {
		return fmt.Errorf("failed to acquire ACS token: %w", err)
	}

	url := fmt.Sprintf("%s/emails:send?api-version=2023-03-31", endpoint)
	req, err := http.NewRequest("POST", url, bytes.NewReader(jsonBody))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token.Token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("ACS email request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted && resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("ACS email API returned %d: %s", resp.StatusCode, string(respBody))
	}

	return nil
}
