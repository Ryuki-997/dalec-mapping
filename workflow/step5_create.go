package workflow

import (
	"encoding/base64"
	"fmt"
	"os"

	"dalec-mapping/domain/onboarding"
	"dalec-mapping/domain/repository"
	"dalec-mapping/infrastructure/github"
	"dalec-mapping/utils"
)

// commitFile pushes a single file to the onboard repo via the GitHub Contents API.
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

	existingFile, err := github.FetchJSON(fmt.Sprintf("%s?ref=%s", contentsPath, utils.OnboardBranch))
	if err == nil {
		if sha, ok := existingFile["sha"].(string); ok {
			putPayload["sha"] = sha
		}
	}

	_, err = github.WriteJSON(contentsPath, repository.PUT, putPayload)
	if err != nil {
		return fmt.Errorf("failed to commit %s via GitHub API: %w", filePath, err)
	}
	fmt.Printf("Committed %s to %s\n", filePath, utils.OnboardBranch)
	return nil
}

// GitPush commits the spec file and its sibling Dockerfile/Makefile to the base branch.
func GitPush(onboard *onboarding.OnboardingInfo, tag string) error {
	specRepository := onboard.SpecRepository
	specImageName := onboard.SpecImageName
	// 1. Read the local spec file content
	specContent, err := os.ReadFile(utils.SpecPath)
	if err != nil {
		return fmt.Errorf("failed to read spec file: %w", err)
	}

	// Build the directory prefix for all files.
	var dir string
	if specRepository != "" {
		dir = fmt.Sprintf("specs/%s/%s", specRepository, specImageName)
	} else {
		dir = fmt.Sprintf("specs/%s", specImageName)
	}

	specFile := fmt.Sprintf("%s-%s-specfile.yml", specImageName, tag)

	// 2. Push the spec file.
	if err := commitFile(
		fmt.Sprintf("%s/%s", dir, specFile),
		fmt.Sprintf("Add %s-%s-specfile.yml", specImageName, tag),
		specContent,
	); err != nil {
		return err
	}

	// 3. Push sibling Dockerfile (if present).
	if len(onboard.DockerfileContent) > 0 {
		if err := commitFile(
			fmt.Sprintf("%s/Dockerfile", dir),
			fmt.Sprintf("Add Dockerfile for %s", specImageName),
			onboard.DockerfileContent,
		); err != nil {
			return err
		}
	}

	// 4. Push sibling Makefile (if present).
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