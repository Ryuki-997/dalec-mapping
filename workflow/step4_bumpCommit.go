// ═══════════════════════════════════════════════════════════════════════════════
// Step 4 — Bump Operations
//
//   Handles all spec-bumping paths: detecting whether a revision bump is needed,
//   performing a revision bump (same version, new commit), and performing a
//   commit bump (new version, copy from template).
//
//   Chunk 1 · DETECT REVISION BUMP
//     DetectRevisionBump()
//       → buildCurrentSpecFilePath()
//       → fetchExistingCommit()
//
//   Chunk 2 · BUMP REVISION
//     BumpRevision()
//       → fetchLatestRevisionSpec()
//       → updateCommitOnly()
//
//   Chunk 3 · BUMP COMMIT
//     BumpCommit()
//       → fetchTemplateSpec()
//       → updateSpecArgs()
//
//   Chunk 4 · SHARED HELPERS
//     writeSpecFile()
//     fetchRemoteSpec()
//     findMapValue()
// ═══════════════════════════════════════════════════════════════════════════════

package workflow

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"log"
	"os"
	"strings"

	"dalec-mapping/domain/naming"
	"dalec-mapping/domain/onboarding"
	"dalec-mapping/infrastructure/github"
	"dalec-mapping/infrastructure/semver"
	"dalec-mapping/pipeline"
	"dalec-mapping/utils"

	"gopkg.in/yaml.v3"
)

// ─── Chunk 1 · DETECT REVISION BUMP ─────────────────────────────────────────

// DetectRevisionBump checks whether the current tag's commit differs from the
// latest existing spec for the same version. Returns true if a revision bump is
// needed (spec exists but commit hash changed), false otherwise.
// When the spec file doesn't exist in existingPaths, returns (false, nil) —
// meaning no revision bump is applicable (either first onboard or new version).
func DetectRevisionBump(existingPaths map[string]bool) (bool, error) {
	specFilePath := buildCurrentSpecFilePath()

	if !existingPaths[specFilePath] {
		return false, nil
	}

	existingCommit, err := fetchExistingCommit(specFilePath)
	if err != nil {
		return false, fmt.Errorf("failed to read existing spec commit: %w", err)
	}

	onboard := pipeline.Current.Onboard
	tagSet := pipeline.Current.Tag

	currentCommit, err := pipeline.LookupTagCommit(onboard.Repository, tagSet.Full)
	if err != nil {
		return false, fmt.Errorf("failed to resolve current commit for tag %s: %w", tagSet.Full, err)
	}

	if existingCommit == currentCommit {
		log.Printf("   Spec already up to date for %s @ %s (commit %s)\n", onboard.SpecImageName, tagSet.Stripped, currentCommit)
		return false, nil
	}

	log.Printf("   Revision bump needed for %s @ %s: commit %s → %s\n",
		onboard.SpecImageName, tagSet.Stripped, existingCommit, currentCommit)
	return true, nil
}

// buildCurrentSpecFilePath computes the expected SpecFilePath for the current
// pipeline state using Naming resolution.
func buildCurrentSpecFilePath() string {
	state := onboarding.ComponentState{
		Onboard: pipeline.Current.Onboard,
		Tag:     pipeline.Current.Tag,
	}
	resolved := naming.Resolve(state)
	return resolved.SpecFilePath
}

// fetchExistingCommit fetches the existing spec from the onboard repo and
// extracts the args.COMMIT value.
func fetchExistingCommit(specFilePath string) (string, error) {
	specNode, err := fetchRemoteSpec(specFilePath)
	if err != nil {
		return "", err
	}

	argsNode := findMapValue(specNode, "args")
	if argsNode == nil {
		return "", fmt.Errorf("existing spec %s missing 'args' section", specFilePath)
	}

	commitNode := findMapValue(argsNode, "COMMIT")
	if commitNode == nil {
		return "", fmt.Errorf("existing spec %s missing args.COMMIT", specFilePath)
	}

	return commitNode.Value, nil
}

// ─── Chunk 2 · BUMP REVISION ────────────────────────────────────────────────

// BumpRevision copies the latest existing spec for the same version, updates
// only args.COMMIT with the new tag's commit, and writes the result to
// utils.SpecPath. The revision number is already incremented in TagSet by step 2.
func BumpRevision() error {
	onboard := pipeline.Current.Onboard
	tagSet := pipeline.Current.Tag

	log.Printf("Revision bump for %s @ %s R%d\n", onboard.SpecImageName, tagSet.Stripped, tagSet.Revision)

	specNode, err := fetchLatestRevisionSpec()
	if err != nil {
		return err
	}

	newCommit, err := pipeline.LookupTagCommit(onboard.Repository, tagSet.Full)
	if err != nil {
		return fmt.Errorf("failed to resolve commit for tag %s: %w", tagSet.Full, err)
	}
	log.Printf("   Commit SHA (from cache): %s\n", newCommit)

	if err := updateCommitOnly(specNode, newCommit); err != nil {
		return err
	}

	if err := writeSpecFile(specNode); err != nil {
		return err
	}

	log.Printf("✅ Revision bump complete — written to %s\n", utils.SpecPath)
	return nil
}

// fetchLatestRevisionSpec fetches the previous revision's spec for the same
// version from the onboard repo.
func fetchLatestRevisionSpec() (*yaml.Node, error) {
	onboard := pipeline.Current.Onboard
	tagSet := pipeline.Current.Tag

	previousRevision := tagSet.Revision - 1
	specDir := onboard.SpecDir()
	remotePath := semver.SpecFilePath(specDir, onboard.SpecImageName, tagSet.Stripped, previousRevision)

	log.Printf("   Template (same version, R%d): %s\n", previousRevision, remotePath)
	return fetchRemoteSpec(remotePath)
}

// updateCommitOnly updates only args.COMMIT in the parsed YAML node.
// VERSION is left unchanged since the version has not changed.
func updateCommitOnly(specNode *yaml.Node, newCommit string) error {
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
	return nil
}

// ─── Chunk 3 · BUMP COMMIT ──────────────────────────────────────────────────

// BumpCommit copies a previous version's spec (the template), updates
// args.COMMIT and args.VERSION for the new tag, and writes the result to
// utils.SpecPath. Template is derived from pipeline.Current state.
func BumpCommit() error {
	onboard := pipeline.Current.Onboard
	tagSet := pipeline.Current.Tag

	templateRevision := tagSet.Revision - 1
	specDir := onboard.SpecDir()
	templateRemotePath := semver.SpecFilePath(specDir, onboard.SpecImageName, semver.ToTag(tagSet.Full), templateRevision)

	log.Printf("Commit bump for %s @ %s R%d (template: %s)\n", onboard.SpecImageName, tagSet.Stripped, tagSet.Revision, templateRemotePath)

	specNode, err := fetchRemoteSpec(templateRemotePath)
	if err != nil {
		return fmt.Errorf("failed to fetch template spec: %w", err)
	}

	newCommit, err := pipeline.LookupTagCommit(onboard.Repository, tagSet.Full)
	if err != nil {
		return fmt.Errorf("failed to resolve commit for tag %s: %w", tagSet.Full, err)
	}
	log.Printf("   Commit SHA (from cache): %s\n", newCommit)

	if err := updateSpecArgs(specNode, tagSet.Stripped, newCommit); err != nil {
		return err
	}

	if err := writeSpecFile(specNode); err != nil {
		return err
	}

	log.Printf("✅ Commit bump complete — written to %s\n", utils.SpecPath)
	return nil
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

// ─── Chunk 4 · SHARED HELPERS ───────────────────────────────────────────────

// writeSpecFile marshals the YAML node and writes it to the local spec path.
func writeSpecFile(specNode *yaml.Node) error {
	var buf bytes.Buffer
	encoder := yaml.NewEncoder(&buf)
	encoder.SetIndent(2)
	if err := encoder.Encode(specNode); err != nil {
		return fmt.Errorf("failed to marshal updated spec: %w", err)
	}
	encoder.Close()
	if err := os.MkdirAll(utils.ResultDir, 0755); err != nil {
		return fmt.Errorf("failed to create result directory: %w", err)
	}
	if err := os.WriteFile(utils.SpecPath, buf.Bytes(), 0644); err != nil {
		return fmt.Errorf("failed to write updated spec: %w", err)
	}
	return nil
}

// fetchRemoteSpec fetches and decodes a spec file from the onboard repo at the
// given path. Returns the parsed YAML document node.
func fetchRemoteSpec(remotePath string) (*yaml.Node, error) {
	fileData, err := github.FetchJSON(fmt.Sprintf("repos/%s/%s/contents/%s?ref=%s",
		utils.OnboardOwner, utils.OnboardRepo, remotePath, utils.OnboardBranch))
	if err != nil {
		return nil, fmt.Errorf("failed to fetch spec %s: %w", remotePath, err)
	}

	contentStr, ok := fileData["content"].(string)
	if !ok {
		return nil, fmt.Errorf("unexpected response: missing content field for %s", remotePath)
	}

	specBytes, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(contentStr, "\n", ""))
	if err != nil {
		return nil, fmt.Errorf("failed to decode spec content: %w", err)
	}

	var specNode yaml.Node
	if err := yaml.Unmarshal(specBytes, &specNode); err != nil {
		return nil, fmt.Errorf("failed to parse spec YAML: %w", err)
	}

	return &specNode, nil
}

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
