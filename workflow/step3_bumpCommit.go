// ═══════════════════════════════════════════════════════════════════════════════
// Step 3 — Bump Commit
//
//   Fast-path for re-onboards where the Dockerfile/Makefile haven't changed.
//   Copies a previous tag's spec (templateTag), updates args.COMMIT and
//   args.VERSION for the new tag, and writes a new spec file locally for push.
//
//   Chunk 1 · MAIN   BumpCommit()
//   Chunk 2 · HELPER findMapValue()
// ═══════════════════════════════════════════════════════════════════════════════

package workflow

import (
	"encoding/base64"
	"fmt"
	"log"
	"os"
	"strings"

	"dalec-mapping/domain/onboarding"
	"dalec-mapping/infrastructure/repository"
	"dalec-mapping/infrastructure/semver"
	"dalec-mapping/utils"

	"gopkg.in/yaml.v3"
)

// ─── Chunk 1 · MAIN ─────────────────────────────────────────────────────────

// BumpCommit copies a previous tag's spec (templateTag), updates args.COMMIT
// and args.VERSION for the new tag, and writes the result to utils.SpecPath.
func BumpCommit(onboard *onboarding.OnboardingInfo, fullTag string, templateTag string) (string, error) {
	tag := semver.ToTag(fullTag)
	remotePath := semver.SpecFilePath(onboard.SpecRepository, onboard.SpecImageName, tag)

	templateRemotePath := semver.SpecFilePath(onboard.SpecRepository, onboard.SpecImageName, semver.ToTag(templateTag))
	log.Printf("🔄 Commit bump for %s @ %s (template: %s → new: %s)\n", onboard.SpecImageName, tag, templateRemotePath, remotePath)

	// Fetch the template spec from the onboard repo
	fileData, err := repository.FetchJSON(fmt.Sprintf("repos/%s/%s/contents/%s?ref=%s",
		utils.OnboardOwner, utils.OnboardRepo, templateRemotePath, utils.OnboardBranch))
	if err != nil {
		return "", fmt.Errorf("failed to fetch template spec %s: %w", templateRemotePath, err)
	}
	contentStr, ok := fileData["content"].(string)
	if !ok {
		return "", fmt.Errorf("unexpected response: missing content field for %s", templateRemotePath)
	}

	// Decode base64 spec content
	specBytes, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(contentStr, "\n", ""))
	if err != nil {
		return "", fmt.Errorf("failed to decode spec content: %w", err)
	}

	// Parse as ordered YAML to preserve structure
	var specNode yaml.Node
	if err := yaml.Unmarshal(specBytes, &specNode); err != nil {
		return "", fmt.Errorf("failed to parse existing spec YAML: %w", err)
	}

	// Resolve the new commit SHA for this tag
	var newCommit string
	if repository.IsADORepo(onboard.Repository) {
		newCommit, err = repository.FetchADOTagCommit(onboard.Repository, fullTag)
	} else {
		owner, repoName, _ := repository.FetchRepositorySegments(onboard.Repository)
		newCommit, err = repository.FetchTagCommit(owner, repoName, fullTag)
	}
	if err != nil {
		return "", fmt.Errorf("failed to resolve commit for tag %s: %w", fullTag, err)
	}
	log.Printf("   New commit SHA: %s\n", newCommit)

	// Update args.COMMIT only
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

	// Update args.VERSION to the new tag's version
	versionNode := findMapValue(argsNode, "VERSION")
	if versionNode != nil {
		newVersion := strings.TrimPrefix(tag, "v")
		log.Printf("   VERSION: %s → %s\n", versionNode.Value, newVersion)
		versionNode.Value = newVersion
	}

	// Marshal back and write to local spec path
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

// ─── Chunk 2 · HELPER ───────────────────────────────────────────────────────

// findMapValue searches a YAML node tree for a mapping key and returns its value node.
func findMapValue(root *yaml.Node, key string) *yaml.Node {
	if root == nil {
		return nil
	}
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
