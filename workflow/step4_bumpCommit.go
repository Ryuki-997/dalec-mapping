// ═══════════════════════════════════════════════════════════════════════════════
// Step 4 — Bump Operations
//
//   Handles all spec-bumping paths: detecting whether a revision bump is needed,
//   performing a revision bump (same version, new commit), and performing a
//   version bump (new version, copy from template).
//
//   Chunk 1 · DETECT REVISION BUMP
//     DetectRevisionBump()
//       → buildCurrentSpecFilePath()
//
//   Chunk 2 · BUMP REVISION
//     BumpRevision()
//       → updateCommitOnly()
//
//   Chunk 3 · BUMP VERSION
//     BumpCommit()
//       → updateSpecArgs()
//
//   Chunk 4 · SHARED HELPERS
//     writeSpecFile()
// ═══════════════════════════════════════════════════════════════════════════════

package workflow

import (
	"bytes"
	"fmt"
	"log"
	"os"
	"strings"

	"dalec-mapping/domain/naming"
	"dalec-mapping/domain/onboarding"
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

	existingCommit, err := utils.SpecRepoFetchCommit(specFilePath)
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

// ─── Chunk 2 · BUMP REVISION ────────────────────────────────────────────────

// BumpRevision copies the latest existing spec for the same version, updates
// only args.COMMIT with the new tag's commit, and writes the result to
// utils.SpecPath. The revision number is already incremented in TagSet by step 2.
func BumpRevision() error {
	onboard := pipeline.Current.Onboard
	tagSet := pipeline.Current.Tag

	log.Printf("Revision bump for %s @ %s R%d\n", onboard.SpecImageName, tagSet.Stripped, tagSet.Revision)

	specNode, err := utils.SpecRepoFetchLatestRevision(onboard.SpecDir(), onboard.SpecImageName, tagSet.Stripped, tagSet.Revision)
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

// updateCommitOnly updates only args.COMMIT in the parsed YAML node.
// VERSION is left unchanged since the version has not changed.
func updateCommitOnly(specNode *yaml.Node, newCommit string) error {
	argsNode := utils.FindMapValue(specNode, "args")
	if argsNode == nil {
		return fmt.Errorf("spec file missing 'args' section")
	}

	commitNode := utils.FindMapValue(argsNode, "COMMIT")
	if commitNode == nil {
		return fmt.Errorf("spec file missing args.COMMIT")
	}

	log.Printf("   COMMIT: %s → %s\n", commitNode.Value, newCommit)
	commitNode.Value = newCommit
	return nil
}

// ─── Chunk 3 · BUMP VERSION ─────────────────────────────────────────────────

// BumpVersion finds the latest existing spec for this component (any version),
// copies it as a template, updates args.COMMIT and args.VERSION for the new tag,
// and writes the result to utils.SpecPath.
func BumpVersion(existingPaths map[string]bool) error {
	onboard := pipeline.Current.Onboard
	tagSet := pipeline.Current.Tag
	specDir := onboard.SpecDir()

	templatePath, found := utils.SpecRepoFindLatestVersion(specDir, onboard.SpecImageName, existingPaths)
	if !found {
		return fmt.Errorf("no existing spec found for %s to use as template", onboard.SpecImageName)
	}

	log.Printf("Version bump for %s @ %s (template: %s)\n", onboard.SpecImageName, tagSet.Stripped, templatePath)

	specNode, err := utils.SpecRepoFetchSpec(templatePath)
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

	log.Printf("✅ Version bump complete — written to %s\n", utils.SpecPath)
	return nil
}

// updateSpecArgs updates args.COMMIT and args.VERSION in the parsed YAML node.
func updateSpecArgs(specNode *yaml.Node, tag, newCommit string) error {
	argsNode := utils.FindMapValue(specNode, "args")
	if argsNode == nil {
		return fmt.Errorf("spec file missing 'args' section")
	}

	commitNode := utils.FindMapValue(argsNode, "COMMIT")
	if commitNode == nil {
		return fmt.Errorf("spec file missing args.COMMIT")
	}
	log.Printf("   COMMIT: %s → %s\n", commitNode.Value, newCommit)
	commitNode.Value = newCommit

	versionNode := utils.FindMapValue(argsNode, "VERSION")
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
