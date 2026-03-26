// ═══════════════════════════════════════════════════════════════════════════════
// Step 6 — Push to Remote
//
//   Commits the generated spec file and sibling Dockerfile/Makefile to the
//   onboard repo via the GitHub Contents API.
//
//   Chunk 1 · MAIN   PushToRemote()
//   Chunk 2 · HELPER commitFile()
// ═══════════════════════════════════════════════════════════════════════════════

package workflow

import (
	"encoding/base64"
	"fmt"
	"os"

	"dalec-mapping/domain/onboarding"
	repo "dalec-mapping/domain/repository"
	"dalec-mapping/infrastructure/repository"
	"dalec-mapping/utils"
)

// ─── Chunk 1 · MAIN ─────────────────────────────────────────────────────────

// PushToRemote commits the spec file and its sibling Dockerfile/Makefile to the
// onboard repo's base branch.
func PushToRemote(onboard *onboarding.OnboardingInfo, tag string) error {
	specRepository := onboard.SpecRepository
	specImageName := onboard.SpecImageName

	// Read the local spec file
	specContent, err := os.ReadFile(utils.SpecPath)
	if err != nil {
		return fmt.Errorf("failed to read spec file: %w", err)
	}

	// Build the directory prefix for all files
	var dir string
	if specRepository != "" {
		dir = fmt.Sprintf("specs/%s/%s", specRepository, specImageName)
	} else {
		dir = fmt.Sprintf("specs/%s", specImageName)
	}

	specFile := fmt.Sprintf("%s-%s-specfile.yml", specImageName, tag)

	// Push the spec file
	if err := commitFile(
		fmt.Sprintf("%s/%s", dir, specFile),
		fmt.Sprintf("Add %s-%s-specfile.yml", specImageName, tag),
		specContent,
	); err != nil {
		return err
	}

	// Push sibling Dockerfile (if present)
	if len(onboard.DockerfileContent) > 0 {
		if err := commitFile(
			fmt.Sprintf("%s/Dockerfile", dir),
			fmt.Sprintf("Add Dockerfile for %s", specImageName),
			onboard.DockerfileContent,
		); err != nil {
			return err
		}
	}

	// Push sibling Makefile (if present)
	if len(onboard.MakefileContent) > 0 {
		if err := commitFile(
			fmt.Sprintf("%s/Makefile", dir),
			fmt.Sprintf("Add Makefile for %s", specImageName),
			onboard.MakefileContent,
		); err != nil {
			return err
		}
	}

	return nil
}

// ─── Chunk 2 · HELPER ───────────────────────────────────────────────────────

// commitFile pushes a single file to the onboard repo via the GitHub Contents API.
// If the file already exists, its SHA is included to perform an update.
func commitFile(filePath, message string, content []byte) error {
	encoded := base64.StdEncoding.EncodeToString(content)
	contentsPath := fmt.Sprintf("repos/%s/%s/contents/%s", utils.OnboardOwner, utils.OnboardRepo, filePath)
	putPayload := map[string]interface{}{
		"message": message,
		"committer": map[string]string{
			"name":  "spec generation",
			"email": "ryukikoda@microsoft.com",
		},
		"content": encoded,
		"branch":  utils.OnboardBranch,
	}

	// Include SHA for updates to existing files
	existingFile, err := repository.FetchJSON(fmt.Sprintf("%s?ref=%s", contentsPath, utils.OnboardBranch))
	if err == nil {
		if sha, ok := existingFile["sha"].(string); ok {
			putPayload["sha"] = sha
		}
	}

	_, err = repository.WriteJSON(contentsPath, repo.PUT, putPayload)
	if err != nil {
		return fmt.Errorf("failed to commit %s via GitHub API: %w", filePath, err)
	}
	fmt.Printf("Committed %s to %s\n", filePath, utils.OnboardBranch)
	return nil
}