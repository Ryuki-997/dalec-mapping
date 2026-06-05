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

	"dalec-mapping/domain/pathcache"
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
// component.Revision is the NEXT revision to create; this function
// inspects the prior revision (Revision-1). Callers should only invoke this when
// Revision > 1 (i.e. a prior same-version spec is known to exist).
func DetectRevisionBump(component *workplan.WorkComponent) (bool, error) {
	componentNaming := component.Naming
	tagSet := component.Tag

	priorRevision := component.Revision - 1
	if priorRevision < 1 {
		priorRevision = 1
	}
	specFilePath := componentNaming.SpecFilePathAt(tagSet.Version, priorRevision)

	if !pathcache.Has(specFilePath) {
		return false, nil
	}

	existingCommit, err := specapi.SpecRepoFetchCommit(specFilePath)
	if err != nil {
		return false, fmt.Errorf("failed to read existing spec commit: %w", err)
	}

	currentCommit, err := tagcache.LookupCommit(tagSet.Full)
	if err != nil {
		return false, fmt.Errorf("failed to resolve current commit for tag %s: %w", tagSet.Full, err)
	}

	if existingCommit == currentCommit {
		log.Printf("   Spec already up to date for %s (commit %s)\n", componentNaming.SpecFileName, currentCommit)
		return false, nil
	}

	log.Printf("   Revision bump needed for %s: commit %s → %s\n",
		componentNaming.SpecFileName, existingCommit, currentCommit)
	return true, nil
}

// ─── Chunk 2 · BUMP REVISION ────────────────────────────────────────────────

// BumpRevision copies the prior revision's spec for the same version, updates
// only args.COMMIT with the new tag's commit, and returns the encoded spec
// bytes. The revision number is already incremented on the WorkComponent by step 2,
// so the prior revision sits at component.Revision - 1.
func BumpRevision(component *workplan.WorkComponent) ([]byte, error) {
	componentNaming := component.Naming
	tagSet := component.Tag

	log.Printf("Revision bump for %s\n", componentNaming.SpecFileName)

	priorRevision := component.Revision - 1
	if priorRevision < 1 {
		priorRevision = 1
	}
	priorPath := componentNaming.SpecFilePathAt(tagSet.Version, priorRevision)
	log.Printf("   Template (same version, R%d): %s\n", priorRevision, priorPath)

	specNode, err := specapi.SpecRepoFetchSpec(priorPath)
	if err != nil {
		return nil, err
	}

	newCommit, err := tagcache.LookupCommit(tagSet.Full)
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

// BumpVersion fetches the highest-revision spec under the given templateMinor
// ("<major>.<minor>") snapshot directory, copies it as a template, updates
// args.COMMIT and args.VERSION for the new tag, and returns the encoded spec
// bytes. templateMinor is the prefix resolved by Phase 2's resolveAction once
// semver.FindTemplateVersion succeeds; the concrete patch and revision of the
// cloned specfile are resolved by scanning pathcache.Cache.
func BumpVersion(component *workplan.WorkComponent, templateMinor string) ([]byte, error) {
	componentNaming := component.Naming
	tagSet := component.Tag
	if templateMinor == "" {
		return nil, fmt.Errorf("template version not provided for %s on %s", componentNaming.SpecImageName, tagSet.Full)
	}

	templateFullVersion, templateRevision, found := semver.FindLatestVersionAndRevision(componentNaming, templateMinor)
	if !found {
		return nil, fmt.Errorf("no specfile found for %s under major.minor %s", componentNaming.SpecImageName, templateMinor)
	}
	templatePath := componentNaming.SpecFilePathAt(templateFullVersion, templateRevision)

	log.Printf("Version bump for %s (template major.minor=%s → %s rev %d)\n",
		componentNaming.SpecFileName, templateMinor, templatePath, templateRevision)

	specNode, err := specapi.SpecRepoFetchSpec(templatePath)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch template spec: %w", err)
	}

	newCommit, err := tagcache.LookupCommit(tagSet.Full)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve commit for tag %s: %w", tagSet.Full, err)
	}
	log.Printf("   Commit SHA (from cache): %s\n", newCommit)

	if err := updateSpecArgs(specNode, tagSet.Version, newCommit); err != nil {
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
// version must already be in numeric form (no leading "v"); TagSet.Version
// is the canonical source.
func updateSpecArgs(specNode *yaml.Node, version, newCommit string) error {
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
		log.Printf("   VERSION: %s → %s\n", versionNode.Value, version)
		versionNode.Value = version
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
