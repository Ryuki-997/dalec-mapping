package tool

import (
	"dalec-mapping/global"
	"encoding/base64"
	"fmt"
	"os"
)

const (
	targetOwner      = "azure-management-and-platforms"
	targetRepo = "aks-dalec-build-defs"
	targetBranch = "ksehgal/fix-publish-poc"
)

// GitPush commits the spec file directly to the base branch.
func GitPush(repo, tag string) error {
	// 1. Read the local spec file content
	specContent, err := os.ReadFile(global.SpecPath)
	if err != nil {
		return fmt.Errorf("failed to read spec file: %w", err)
	}

	owner, repoName, _, _ := global.ExtractRepositorySegments(repo)

	encoded := base64.StdEncoding.EncodeToString(specContent)
	file := fmt.Sprintf("%s-%s-specfile.yml", repoName, tag)
	filePath := fmt.Sprintf("specs/%s/%s/%s", owner, repoName, file)

	// 2. Commit the file directly to the base branch (PUT /repos/{owner}/{repo}/contents/{path})
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
	_, err = global.MakeGitHubRequest[map[string]interface{}](global.GithubRequest{
		URL:     contentsURL,
		Method:  global.PUT,
		Payload: putPayload,
	})
	if err != nil {
		return fmt.Errorf("failed to commit file via GitHub API: %w", err)
	}

	fmt.Printf("Committed to %s\n", targetBranch)
	return nil
}