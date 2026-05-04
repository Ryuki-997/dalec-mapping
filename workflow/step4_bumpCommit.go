// ═══════════════════════════════════════════════════════════════════════════════
// Step 4 — Bump Commit
//
//   Fast-path for re-onboards where the Dockerfile/Makefile haven't changed.
//   Copies a previous tag's spec (templateTag), updates args.COMMIT and
//   args.VERSION for the new tag, and writes a new spec file locally for push.
//
//   Chunk 1 · MAIN    BumpCommit()
//   Chunk 2 · STEPS   fetchTemplateSpec(), resolveNewCommit(), updateSpecArgs(), writeSpecFile()
//   Chunk 3 · HELPER  findMapValue()
// ═══════════════════════════════════════════════════════════════════════════════

package workflow

import (
	"encoding/base64"
	"fmt"
	"log"
	"os"
	"strings"

	"dalec-mapping/infrastructure/repository"
	"dalec-mapping/infrastructure/semver"
	"dalec-mapping/pipeline"
	"dalec-mapping/utils"

	"gopkg.in/yaml.v3"
)

// ─── Chunk 1 · MAIN ─────────────────────────────────────────────────────────

// BumpCommit copies a previous tag's spec (templateTag), updates args.COMMIT
// and args.VERSION for the new tag, and writes the result to utils.SpecPath.
func BumpCommit(templateTag string, templateRevision int) (string, error) {
	onboard := pipeline.Current.Onboard
	tagSet := pipeline.Current.Tag

	specDir := onboard.SpecDir()
	remotePath := semver.SpecFilePath(specDir, onboard.SpecImageName, tagSet.Stripped, tagSet.Revision)
	templateRemotePath := semver.SpecFilePath(specDir, onboard.SpecImageName, semver.ToTag(templateTag), templateRevision)

	log.Printf("Commit bump for %s @ %s R%d (template: %s)\n", onboard.SpecImageName, tagSet.Stripped, tagSet.Revision, templateRemotePath)

	specNode, err := fetchTemplateSpec(templateRemotePath)
	if err != nil {
		return "", err
	}

	newCommit, err := pipeline.LookupTagCommit(onboard.Repository, tagSet.Full)
	if err != nil {
		return "", fmt.Errorf("failed to resolve commit for tag %s: %w", tagSet.Full, err)
	}
	log.Printf("   Commit SHA (from cache): %s\n", newCommit)

	if err := updateSpecArgs(specNode, tagSet.Stripped, newCommit); err != nil {
		return "", err
	}

	if err := writeSpecFile(specNode); err != nil {
		return "", err
	}

	log.Printf("Step 4 output: %s\n", remotePath)
	log.Printf("✅ Commit bump complete — written to %s\n", utils.SpecPath)
	return remotePath, nil
}

// ─── Chunk 2 · STEPS ─────────────────────────────────────────────────────────

// fetchTemplateSpec fetches and decodes a previous tag's spec from the onboard repo.
func fetchTemplateSpec(templateRemotePath string) (*yaml.Node, error) {
	fileData, err := repository.FetchJSON(fmt.Sprintf("repos/%s/%s/contents/%s?ref=%s",
		utils.OnboardOwner, utils.OnboardRepo, templateRemotePath, utils.OnboardBranch))
	if err != nil {
		return nil, fmt.Errorf("failed to fetch template spec %s: %w", templateRemotePath, err)
	}

	contentStr, ok := fileData["content"].(string)
	if !ok {
		return nil, fmt.Errorf("unexpected response: missing content field for %s", templateRemotePath)
	}

	specBytes, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(contentStr, "\n", ""))
	if err != nil {
		return nil, fmt.Errorf("failed to decode spec content: %w", err)
	}

	var specNode yaml.Node
	if err := yaml.Unmarshal(specBytes, &specNode); err != nil {
		return nil, fmt.Errorf("failed to parse existing spec YAML: %w", err)
	}

	return &specNode, nil
}

// updateSpecArgs updates args.COMMIT and args.VERSION in the parsed YAML node.
func updateSpecArgs(specNode *yaml.Node, tag, newCommit string) error {
	argsNode := findMapValue(specNode, "args")
	if argsNode == nil {
		return fmt.Errorf("spec file missing 'args' section")
	}

	commitNode := findMapValue(argsNode, "COMMIT")
	if commitNode == nil {
		return fmt.Errorf("spec file missing args.COMMIT")
	}
	log.Printf("   COMMIT: %s → %s\n", commitNode.Value, newCommit)
	commitNode.Value = newCommit

	versionNode := findMapValue(argsNode, "VERSION")
	if versionNode != nil {
		newVersion := strings.TrimPrefix(tag, "v")
		log.Printf("   VERSION: %s → %s\n", versionNode.Value, newVersion)
		versionNode.Value = newVersion
	}

	return nil
}

// writeSpecFile marshals the YAML node and writes it to the local spec path.
func writeSpecFile(specNode *yaml.Node) error {
	out, err := yaml.Marshal(specNode)
	if err != nil {
		return fmt.Errorf("failed to marshal updated spec: %w", err)
	}
	if err := os.MkdirAll(utils.ResultDir, 0755); err != nil {
		return fmt.Errorf("failed to create result directory: %w", err)
	}
	if err := os.WriteFile(utils.SpecPath, out, 0644); err != nil {
		return fmt.Errorf("failed to write updated spec: %w", err)
	}
	return nil
}

// ─── Chunk 3 · HELPER ───────────────────────────────────────────────────────

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
