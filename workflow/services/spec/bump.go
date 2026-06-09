// ═══════════════════════════════════════════════════════════════════════════════
// Bump — Bump Operations
//
//   Performs the two spec-bumping paths chosen by Phase 2's resolveAction:
//   a revision bump (same version, new commit) and a version bump (new
//   version, copy from same-major template).
//
//   Chunk 1 · BUMP REVISION
//     BumpRevision()
//       → updateCommitOnly()
//
//   Chunk 2 · BUMP VERSION
//     BumpVersion()
//       → updateSpecArgs()
//
//   Chunk 3 · SHARED HELPERS
//     encodeSpec()
// ═══════════════════════════════════════════════════════════════════════════════

package spec

import (
	"bytes"
	"fmt"
	"log"

	"dalec-mapping/domain/tagcache"
	"dalec-mapping/domain/workplan"
	"dalec-mapping/workflow/infrastructure/specapi"

	"gopkg.in/yaml.v3"
)

// ─── Chunk 1 · BUMP REVISION ────────────────────────────────────────────────

// BumpRevision copies the prior revision's spec for the same version, updates
// only args.COMMIT with the new tag's commit, and returns the encoded spec
// bytes. The new revision is already set on the WorkComponent by Phase 2's
// resolveAction, so the prior revision sits at component.Revision - 1.
func BumpRevision(component *workplan.WorkComponent) ([]byte, error) {
	componentNaming := component.Naming
	tagSet := component.Tag

	priorRevision := component.Revision - 1
	if priorRevision < 1 {
		priorRevision = 1
	}
	priorPath := fmt.Sprintf("%s/%s-%s-%d-specfile.yml",
		componentNaming.OnboardDir, componentNaming.SpecImageName, tagSet.Version, priorRevision)

	specNode, err := specapi.SpecRepoFetchSpec(priorPath)
	if err != nil {
		return nil, err
	}

	newCommit, err := tagcache.LookupCommit(tagSet.Full)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve commit for tag %s: %w", tagSet.Full, err)
	}

	if err := updateCommitOnly(specNode, newCommit); err != nil {
		return nil, err
	}

	log.Printf("  Revision bump: %s (R%d -> R%d, sha=%s)",
		componentNaming.SpecFileName, priorRevision, component.Revision, shortSHA(newCommit))

	return encodeSpec(specNode)
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

	commitNode.Value = newCommit
	return nil
}

// ─── Chunk 2 · BUMP VERSION ─────────────────────────────────────────────────

// BumpVersion clones the spec for the supplied template "<version>-<revision>"
// key, updates args.COMMIT and args.VERSION for the new tag, and returns the
// encoded spec bytes. templateKey is the snapshot key resolved by Phase 2's
// resolveAction once semver.FindTemplateVersion succeeds; the template
// specfile lives next to the snapshot under
// "<OnboardDir>/<SpecImageName>-<templateKey>-specfile.yml".
func BumpVersion(component *workplan.WorkComponent, templateKey string) ([]byte, error) {
	componentNaming := component.Naming
	tagSet := component.Tag
	if templateKey == "" {
		return nil, fmt.Errorf("template key not provided for %s on %s", componentNaming.SpecImageName, tagSet.Full)
	}

	templatePath := fmt.Sprintf("%s/%s-%s-specfile.yml",
		componentNaming.OnboardDir, componentNaming.SpecImageName, templateKey)

	specNode, err := specapi.SpecRepoFetchSpec(templatePath)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch template spec: %w", err)
	}

	newCommit, err := tagcache.LookupCommit(tagSet.Full)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve commit for tag %s: %w", tagSet.Full, err)
	}

	if err := updateSpecArgs(specNode, tagSet.Version, newCommit); err != nil {
		return nil, err
	}

	log.Printf("  Version bump: %s (template=%s -> %s, sha=%s)",
		componentNaming.SpecFileName, templateKey, tagSet.Version, shortSHA(newCommit))

	return encodeSpec(specNode)
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
	commitNode.Value = newCommit

	versionNode := specapi.FindMapValue(argsNode, "VERSION")
	if versionNode != nil {
		versionNode.Value = version
	}

	return nil
}

// ─── Chunk 3 · SHARED HELPERS ───────────────────────────────────────────────

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

// shortSHA returns the first 7 chars of a commit SHA, or the original when
// shorter. Used purely for log line brevity.
func shortSHA(sha string) string {
	if len(sha) <= 7 {
		return sha
	}
	return sha[:7]
}
