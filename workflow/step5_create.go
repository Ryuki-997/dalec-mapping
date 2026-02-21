package workflow

import (
	"encoding/base64"
	"fmt"
	"os"
	"strings"

	"dalec-mapping/domain/repository"
	"dalec-mapping/infrastructure/github"
	"dalec-mapping/utils"
)

const (
	targetOwner      	= "azure-management-and-platforms"
	targetRepo 				= "aks-dalec-build-defs"
	targetBranch 			= "ksehgal/fix-publish-poc"
)

// GitPush commits the spec file directly to the base branch.
func GitPush(repo, tag string) error {
	// 1. Read the local spec file content
	specContent, err := os.ReadFile(utils.SpecPath)
	if err != nil {
		return fmt.Errorf("failed to read spec file: %w", err)
	}

	_, repoName, _, _ := github.ExtractRepositorySegments(repo)
	tag = strings.TrimPrefix(tag, "v")

	encoded := base64.StdEncoding.EncodeToString(specContent)
	file := fmt.Sprintf("%s-%s-specfile.yml", repoName, tag)
	filePath := fmt.Sprintf("specs/%s/%s", repoName, file)

	// 2. Check if file exists and get SHA
	contentsURL := fmt.Sprintf("https://api.github.com/repos/%s/%s/contents/%s", targetOwner, targetRepo, filePath)
	putPayload := map[string]interface{}{
		"message": fmt.Sprintf("Add %s-%s-specfile.yml", repoName, tag),
		"committer": map[string]string{
			"name":  "spec generation",
			"email": "ryukikoda@microsoft.com",
		},
		"content": encoded,
		"branch":  targetBranch,
	}

	// Try to get existing file SHA
	existingFile, err := github.MakeGitHubRequest[map[string]interface{}](repository.GithubRequest{
		URL: fmt.Sprintf("%s?ref=%s", contentsURL, targetBranch),
	})

	// use existing file's SHA if it exists 
	if err == nil {
		if sha, ok := existingFile["sha"].(string); ok {
			putPayload["sha"] = sha
		}
	}

	// 3. Commit the file  
	_, err = github.MakeGitHubRequest[map[string]interface{}](repository.GithubRequest{
		URL:     contentsURL,
		Method:  repository.PUT,
		Payload: putPayload,
	})
	if err != nil {
		return fmt.Errorf("failed to commit file via GitHub API: %w", err)
	}

	fmt.Printf("Committed %s to %s\n", filePath, targetBranch)
	return nil
}