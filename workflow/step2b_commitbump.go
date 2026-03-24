package workflow

import (
	"encoding/base64"
	"fmt"
	"log"
	"os"
	"strings"

	"dalec-mapping/domain/onboarding"
	"dalec-mapping/infrastructure/github"
	"dalec-mapping/infrastructure/semver"
	"dalec-mapping/utils"

	"gopkg.in/yaml.v3"
)

// CommitBump fetches the existing spec file for the given tag, updates
// args.COMMIT to the new tag's commit SHA, and writes the result to
// utils.SpecPath for subsequent push.
func CommitBump(onboard *onboarding.OnboardingInfo, tag string) (string, error) {
	shortTag := semver.StripToSemver(tag)
	remotePath := specFilePath(onboard.SpecRepository, onboard.SpecImageName, shortTag)

	log.Printf("🔄 Commit bump for %s @ %s (spec: %s)\n", onboard.SpecImageName, shortTag, remotePath)

	// 1. Fetch existing spec content from the onboard repo.
	fileData, err := github.FetchJSON(fmt.Sprintf("repos/%s/%s/contents/%s?ref=%s",
		utils.OnboardOwner, utils.OnboardRepo, remotePath, utils.OnboardBranch))
	if err != nil {
		return "", fmt.Errorf("failed to fetch existing spec %s: %w", remotePath, err)
	}

	contentStr, ok := fileData["content"].(string)
	if !ok {
		return "", fmt.Errorf("unexpected response: missing content field for %s", remotePath)
	}

	specBytes, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(contentStr, "\n", ""))
	if err != nil {
		return "", fmt.Errorf("failed to decode spec content: %w", err)
	}

	// 2. Parse as ordered YAML to preserve structure.
	var specNode yaml.Node
	if err := yaml.Unmarshal(specBytes, &specNode); err != nil {
		return "", fmt.Errorf("failed to parse existing spec YAML: %w", err)
	}

	// 3. Resolve the new commit SHA for this tag.
	owner, repo, _ := github.FetchRepositorySegments(onboard.Repository)
	newCommit, err := github.FetchTagCommit(owner, repo, tag)
	if err != nil {
		return "", fmt.Errorf("failed to resolve commit for tag %s: %w", tag, err)
	}
	log.Printf("   New commit SHA: %s\n", newCommit)

	// 4. Update args.COMMIT only.
	argsNode := findMapValue(&specNode, "args")
	if argsNode == nil {
		return "", fmt.Errorf("spec file missing 'args' section")
	}

	commitNode := findMapValue(argsNode, "COMMIT")
	if commitNode == nil {
		return "", fmt.Errorf("spec file missing args.COMMIT")
	}
	log.Printf("   COMMIT: %s → %s\n", commitNode.Value, newCommit)
	commitNode.Value = newCommit

	// 5. Marshal back and write to local spec path.
	out, err := yaml.Marshal(&specNode)
	if err != nil {
		return "", fmt.Errorf("failed to marshal updated spec: %w", err)
	}

	if err := os.MkdirAll(utils.ResultDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create result directory: %w", err)
	}
	if err := os.WriteFile(utils.SpecPath, out, 0644); err != nil {
		return "", fmt.Errorf("failed to write updated spec: %w", err)
	}

	log.Printf("✅ Commit bump complete — written to %s\n", utils.SpecPath)
	return remotePath, nil
}

// findMapValue searches a YAML node tree for a mapping key and returns its value node.
func findMapValue(root *yaml.Node, key string) *yaml.Node {
	if root == nil {
		return nil
	}
	// Document node — recurse into content.
	if root.Kind == yaml.DocumentNode {
		for _, child := range root.Content {
			if result := findMapValue(child, key); result != nil {
				return result
			}
		}
		return nil
	}
	if root.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == key {
			return root.Content[i+1]
		}
	}
	return nil
}
