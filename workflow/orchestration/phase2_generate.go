package orchestration

import (
	"bytes"
	"log"
	"regexp"

	"dalec-mapping/domain/buildresult"
	"dalec-mapping/domain/pathcache"
	"dalec-mapping/domain/tagcache"
	"dalec-mapping/domain/workplan"
	"dalec-mapping/workflow/foundations/logging"
	"dalec-mapping/workflow/infrastructure/semver"
	"dalec-mapping/workflow/infrastructure/specapi"
	"dalec-mapping/workflow/services/partnerrepo"
	"dalec-mapping/workflow/services/spec"
)

// ═══════════════════════════════════════════════════════════════════════════════
// Phase 2 — Generate
//
//   Input:  []workplan.WorkGroup
//   Output: none (mutates each component.Result in place)
//
//   Responsibilities:
//     - For each *workplan.WorkComponent in every component of every group,
//       decide skip/bump-revision/bump-version/generate
//     - Execute the chosen action (partnerrepo.DiscoverBuildFiles / spec.Bump* /
//       spec.GenerateSpec)
//     - Write the resulting BuildResult to component.Result. Identity fields
//       (Naming, Tag) populated by Phase 1 are never modified; BuildFiles
//       is filled by the action sub-steps which take *WorkComponent.
// ═══════════════════════════════════════════════════════════════════════════════

// ─── Public phase API ───────────────────────────────────────────────────────

// Generate runs Phase 2 across every component in the supplied groups.
// Components are pointers, so assigning to component.Result here is the single
// mutation Phase 2 makes; the group/component slices are untouched.
func Generate(groups []workplan.WorkGroup) {
	log.Println("═══ Phase 2: Generate ═══")
	for _, group := range groups {
		for _, component := range group.Components {
			GenerateOne(component)
		}
	}
}

// GenerateOne runs Phase 2 for a single *workplan.WorkComponent and writes the
// outcome to component.Result. Exposed for testing and for callers that want
// per-component control. Group-level inputs (partner repo URL, onboard targets,
// license) are reached via component.ParentGroup.
func GenerateOne(component *workplan.WorkComponent) {
	logging.PrintComponentBanner(component)

	action, templateKey := resolveAction(component)
	component.Result = dispatchAction(action, templateKey, component)
}

// ─── Decision step ──────────────────────────────────────────────────────────

type pipelineAction int

const (
	actionSkip         pipelineAction = iota // Spec already up to date
	actionBumpVersion                        // Copy template spec with new commit hash + version
	actionBumpRevision                       // Same version, new commit → increment revision
	actionGenerate                           // Generate spec from scratch
)

// resolveAction is the single decision point for what happens to a (component, tag) pair.
//
// First, look up an existing spec for this exact (component, version) in the
// spec repo's pathcache. If one exists, compare its commit hash against the
// current tag's commit:
//   - same commit       → SKIP          (revision = existing)
//   - different commit  → BUMP REVISION (revision = existing + 1)
//
// If no existing spec for this version exists, the tag is new. Discover the
// partner repo's Dockerfile/Makefile, then check whether a same-major
// (version, revision) snapshot matches the new tag's files:
//   - no snapshot found → GENERATE     (revision = 1)
//   - snapshot matches  → BUMP VERSION (revision = 1)
//   - snapshot differs  → GENERATE     (revision = 1)
//
// On return, component.Revision is finalized for whichever branch was taken,
// and Naming.Construct has been called exactly once so every Generated field
// (SpecFileName, SpecFilePath, BranchName, PRTitle) is ready for Phase 3.
//
// When the returned action is actionBumpVersion, the second return value is
// the chosen template "<version>-<revision>" key (e.g. "1.8.1-1"). For every
// other action it is the empty string.
func resolveAction(component *workplan.WorkComponent) (pipelineAction, string) {
	action, templateKey := decideAction(component)
	component.Naming.Construct(component.Tag, component.Revision, component.ParentGroup.GroupName, component.ParentGroup.PRID)
	return action, templateKey
}

// decideAction routes to the existing-spec or new-spec branch and sets
// component.Revision before returning. Naming.Construct is intentionally NOT
// called here — resolveAction wraps this call and constructs Naming once,
// regardless of branch.
func decideAction(component *workplan.WorkComponent) (pipelineAction, string) {
	existingPath, _, existingRevision, found := semver.FindLatestSpec(component.Naming, regexp.QuoteMeta(component.Tag.Version))
	if found {
		return decideForExistingSpec(component, existingPath, existingRevision)
	}
	return decideForNewSpec(component)
}

// decideForExistingSpec compares the commit recorded in the existing spec
// against the current tag's commit. Equal commits skip (revision pinned to the
// existing revision); different commits bump (revision = existing + 1).
func decideForExistingSpec(component *workplan.WorkComponent, existingPath string, existingRevision int) (pipelineAction, string) {
	existingCommit, err := specapi.SpecRepoFetchCommit(existingPath)
	if err != nil {
		log.Fatalf("❌ failed to read existing spec commit: %v", err)
	}
	currentCommit, err := tagcache.LookupCommit(component.Tag.Full)
	if err != nil {
		log.Fatalf("❌ failed to resolve commit for tag %s: %v", component.Tag.Full, err)
	}

	if existingCommit == currentCommit {
		component.Revision = existingRevision
		log.Printf("  %s @ %s -> SKIP (spec already up to date at R%d)",
			component.Naming.SpecImageName, component.Tag.Stripped, existingRevision)
		return actionSkip, ""
	}

	component.Revision = existingRevision + 1
	log.Printf("  %s @ %s -> BUMP REVISION (commit %s -> %s, R%d -> R%d)",
		component.Naming.SpecImageName, component.Tag.Stripped,
		shortSHA(existingCommit), shortSHA(currentCommit), existingRevision, component.Revision)
	return actionBumpRevision, ""
}

// decideForNewSpec discovers the partner repo's build files for the new tag,
// then diffs them against the highest same-major snapshot to choose between
// BUMP VERSION (snapshot matches) and GENERATE (no snapshot, or differs).
// New-spec branches always use revision 1.
func decideForNewSpec(component *workplan.WorkComponent) (pipelineAction, string) {
	if err := partnerrepo.DiscoverBuildFiles(component); err != nil {
		log.Fatalf("❌ DiscoverBuildFiles failed: %v", err)
	}

	component.Revision = 1

	templateKey, found := semver.FindTemplateVersion(component)
	if !found {
		log.Printf("  %s @ %s -> GENERATE (no template snapshot)",
			component.Naming.SpecImageName, component.Tag.Stripped)
		return actionGenerate, ""
	}

	templateDF := fetchSnapshot(pathcache.BuildDockerfilePath(component.Naming, templateKey))
	templateMF := fetchSnapshot(pathcache.BuildMakefilePath(component.Naming, templateKey))

	matches, reason := buildFilesMatch(templateDF, templateMF, component.BuildFiles.Dockerfile.Source, component.BuildFiles.Makefile.Source)
	if matches {
		log.Printf("  %s @ %s -> BUMP VERSION (template %s matches)",
			component.Naming.SpecImageName, component.Tag.Stripped, templateKey)
		return actionBumpVersion, templateKey
	}

	log.Printf("  %s @ %s -> GENERATE (template %s differs: %s)",
		component.Naming.SpecImageName, component.Tag.Stripped, templateKey, reason)
	return actionGenerate, ""
}

// shortSHA returns the first 7 chars of a commit SHA, or the original when
// shorter. Used purely for log line brevity.
func shortSHA(sha string) string {
	if len(sha) <= 7 {
		return sha
	}
	return sha[:7]
}

// fetchSnapshot reads a BuildFiles snapshot from the spec repo when pathcache
// confirms it exists; returns nil otherwise (no network call) so the diff
// step can treat missing snapshots as "force GENERATE".
func fetchSnapshot(path string) []byte {
	if !pathcache.Has(path) {
		return nil
	}
	content, err := specapi.SpecRepoFetchFile(path)
	if err != nil {
		log.Fatalf("❌ SpecRepoFetchFile(%s) failed: %v", path, err)
	}
	return content
}

// buildFilesMatch reports whether the freshly-fetched partner Dockerfile and
// Makefile byte-equal the template tag's files after trimming trailing
// newlines. On mismatch the second return value names which file(s) differ
// so the caller can include it in the consolidated decision line.
func buildFilesMatch(templateDF, templateMF, freshDF, freshMF []byte) (bool, string) {
	if len(templateDF) == 0 && len(templateMF) == 0 {
		return false, "template has no Dockerfile/Makefile"
	}

	dockerfileDiffers := len(templateDF) > 0 &&
		(len(freshDF) == 0 || !bytes.Equal(bytes.TrimRight(freshDF, "\n"), bytes.TrimRight(templateDF, "\n")))
	makefileDiffers := len(templateMF) > 0 &&
		(len(freshMF) == 0 || !bytes.Equal(bytes.TrimRight(freshMF, "\n"), bytes.TrimRight(templateMF, "\n")))

	switch {
	case dockerfileDiffers && makefileDiffers:
		return false, "Dockerfile+Makefile"
	case dockerfileDiffers:
		return false, "Dockerfile"
	case makefileDiffers:
		return false, "Makefile"
	}
	return true, ""
}

// ─── Action dispatch ────────────────────────────────────────────────────────

// dispatchAction executes the chosen pipeline action and returns the resulting
// BuildResult. The decision banner was already emitted by resolveAction; this
// step only runs the work and confirms readiness.
func dispatchAction(action pipelineAction, templateKey string, component *workplan.WorkComponent) buildresult.BuildResult {
	switch action {
	case actionSkip:
		return buildresult.BuildResult{Outcome: buildresult.OutcomeSkipped}

	case actionBumpRevision:
		specBytes, err := spec.BumpRevision(component)
		if err != nil {
			log.Fatalf("❌ Revision bump failed: %v", err)
		}
		log.Printf("  ✅ Spec ready: %s @ %s-%d", component.Naming.SpecImageName, component.Tag.Version, component.Revision)
		return buildresult.BuildResult{Outcome: buildresult.OutcomeBumpRevision, SpecContent: specBytes}

	case actionBumpVersion:
		specBytes, err := spec.BumpVersion(component, templateKey)
		if err != nil {
			log.Fatalf("❌ Version bump failed: %v", err)
		}
		log.Printf("  ✅ Spec ready: %s @ %s-%d", component.Naming.SpecImageName, component.Tag.Version, component.Revision)
		return buildresult.BuildResult{Outcome: buildresult.OutcomeBumpVersion, SpecContent: specBytes}

	case actionGenerate:
		specBytes, _, err := spec.GenerateSpec(component)
		if err != nil {
			log.Printf("  ⚠️  Skipping %s @ %s: %v", component.Naming.SpecImageName, component.Tag.Full, err)
			return buildresult.BuildResult{Outcome: buildresult.OutcomeFailed}
		}
		log.Printf("  ✅ Spec ready: %s @ %s-%d", component.Naming.SpecImageName, component.Tag.Version, component.Revision)
		return buildresult.BuildResult{Outcome: buildresult.OutcomeGenerated, SpecContent: specBytes}
	}
	return buildresult.BuildResult{Outcome: buildresult.OutcomeUnknown}
}
