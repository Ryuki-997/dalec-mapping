// ═══════════════════════════════════════════════════════════════════════════════
// Bump — Bump Operations
//
//   Handles all spec-bumping paths: detecting whether a revision bump is needed,
//   performing a revision bump (same version, new commit), and performing a
//   version bump (new version, copy from template).
//
//   Chunk 1 · DETECT REVISION BUMP
//     DetectRevisionBump()
//
//   Chunk 2 · BUMP REVISION
//     BumpRevision()
//       → updateCommitOnly()
//
//   Chunk 3 · BUMP VERSION
//     BumpVersion()
//       → updateSpecArgs()
//
//   Chunk 4 · SHARED HELPERS
//     encodeSpec()
// ═══════════════════════════════════════════════════════════════════════════════

package spec

import (
	"bytes"
	"fmt"
	"log"
	"strings"

	"dalec-mapping/domain/tagcache"
	"dalec-mapping/domain/workplan"
	"dalec-mapping/workflow/infrastructure/semver"
	"dalec-mapping/workflow/infrastructure/specapi"

	"gopkg.in/yaml.v3"
)

// ─── Chunk 1 · DETECT REVISION BUMP ─────────────────────────────────────────

// DetectRevisionBump checks whether the current tag's commit differs from the
// latest existing spec for the same version. Returns true if a revision bump is
// needed (spec exists but commit hash changed), false otherwise.
//
// item.Tag.Revision is the NEXT revision to create; this function
// inspects the prior revision (Revision-1). Callers should only invoke this when
// Revision > 1 (i.e. a prior same-version spec is known to exist).
func DetectRevisionBump(item *workplan.WorkItem, existingPaths map[string]bool) (bool, error) {
	component := item.Naming
	tagSet := item.Tag

	priorRevision := tagSet.Revision - 1
	if priorRevision < 1 {
		priorRevision = 1
	}
	specFilePath := semver.SpecFilePath(component, tagSet.Version, priorRevision)

	if !existingPaths[specFilePath] {
		return false, nil
	}

	existingCommit, err := specapi.SpecRepoFetchCommit(specFilePath)
	if err != nil {
		return false, fmt.Errorf("failed to read existing spec commit: %w", err)
	}

	currentCommit, err := tagcache.Lookup(component.Repository, tagSet.Full)
	if err != nil {
		return false, fmt.Errorf("failed to resolve current commit for tag %s: %w", tagSet.Full, err)
	}

	if existingCommit == currentCommit {
		log.Printf("   Spec already up to date for %s @ %s (commit %s)\n", component.SpecImageName, tagSet.Stripped, currentCommit)
		return false, nil
	}

	log.Printf("   Revision bump needed for %s @ %s: commit %s → %s\n",
		component.SpecImageName, tagSet.Stripped, existingCommit, currentCommit)
	return true, nil
}

// ─── Chunk 2 · BUMP REVISION ────────────────────────────────────────────────

// BumpRevision copies the latest existing spec for the same version, updates
// only args.COMMIT with the new tag's commit, and returns the encoded spec
// bytes. The revision number is already incremented in TagSet by step 2.
func BumpRevision(item *workplan.WorkItem, existingPaths map[string]bool) ([]byte, error) {
	component := item.Naming
	tagSet := item.Tag

	log.Printf("Revision bump for %s @ %s R%d\n", component.SpecImageName, tagSet.Stripped, tagSet.Revision)

	specNode, err := specapi.SpecRepoFetchLatestRevision(component, tagSet.Stripped, existingPaths)
	if err != nil {
		return nil, err
	}

	newCommit, err := tagcache.Lookup(component.Repository, tagSet.Full)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve commit for tag %s: %w", tagSet.Full, err)
	}
	log.Printf("   Commit SHA (from cache): %s\n", newCommit)

	if err := updateCommitOnly(specNode, newCommit); err != nil {
		return nil, err
	}

	specBytes, err := encodeSpec(specNode)
	if err != nil {
		return nil, err
	}

	log.Printf("✅ Revision bump complete\n")
	return specBytes, nil
}

// updateCommitOnly updates only args.COMMIT in the parsed YAML node.
// VERSION is left unchanged since the version has not changed.
func updateCommitOnly(specNode *yaml.Node, newCommit string) error {
	argsNode := specapi.FindMapValue(specNode, "args")
	if argsNode == nil {
		return fmt.Errorf("spec file missing 'args' section")
	}

	commitNode := specapi.FindMapValue(argsNode, "COMMIT")
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
// and returns the encoded spec bytes.
func BumpVersion(item *workplan.WorkItem, existingPaths map[string]bool) ([]byte, error) {
	component := item.Naming
	tagSet := item.Tag

	templatePath, found := specapi.SpecRepoFindLatestMinorVersion(component, tagSet.Stripped, existingPaths)
	if !found {
		return nil, fmt.Errorf("no same-minor-version spec found for %s @ %s to use as template", component.SpecImageName, tagSet.Stripped)
	}

	log.Printf("Version bump for %s @ %s (template: %s)\n", component.SpecImageName, tagSet.Stripped, templatePath)

	specNode, err := specapi.SpecRepoFetchSpec(templatePath)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch template spec: %w", err)
	}

	newCommit, err := tagcache.Lookup(component.Repository, tagSet.Full)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve commit for tag %s: %w", tagSet.Full, err)
	}
	log.Printf("   Commit SHA (from cache): %s\n", newCommit)

	if err := updateSpecArgs(specNode, tagSet.Stripped, newCommit); err != nil {
		return nil, err
	}

	specBytes, err := encodeSpec(specNode)
	if err != nil {
		return nil, err
	}

	log.Printf("✅ Version bump complete\n")
	return specBytes, nil
}

// updateSpecArgs updates args.COMMIT and args.VERSION in the parsed YAML node.
func updateSpecArgs(specNode *yaml.Node, tag, newCommit string) error {
	argsNode := specapi.FindMapValue(specNode, "args")
	if argsNode == nil {
		return fmt.Errorf("spec file missing 'args' section")
	}

	commitNode := specapi.FindMapValue(argsNode, "COMMIT")
	if commitNode == nil {
		return fmt.Errorf("spec file missing args.COMMIT")
	}
	log.Printf("   COMMIT: %s → %s\n", commitNode.Value, newCommit)
	commitNode.Value = newCommit

	versionNode := specapi.FindMapValue(argsNode, "VERSION")
	if versionNode != nil {
		newVersion := strings.TrimPrefix(tag, "v")
		log.Printf("   VERSION: %s → %s\n", versionNode.Value, newVersion)
		versionNode.Value = newVersion
	}

	return nil
}

// ─── Chunk 4 · SHARED HELPERS ───────────────────────────────────────────────

// encodeSpec marshals the YAML node and returns the encoded bytes.
func encodeSpec(specNode *yaml.Node) ([]byte, error) {
	var buf bytes.Buffer
	encoder := yaml.NewEncoder(&buf)
	encoder.SetIndent(2)
	if err := encoder.Encode(specNode); err != nil {
		return nil, fmt.Errorf("failed to marshal updated spec: %w", err)
	}
	encoder.Close()
	return buf.Bytes(), nil
}
