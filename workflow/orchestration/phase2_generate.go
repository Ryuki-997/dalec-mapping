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
		log.Printf("Skipping %s @ %s — spec already up to date (R%d)\n",
			component.Naming.SpecImageName, component.Tag.Stripped, existingRevision)
		return actionSkip, ""
	}

	component.Revision = existingRevision + 1
	log.Printf("Revision bump for %s @ %s: commit %s → %s (R%d → R%d)\n",
		component.Naming.SpecImageName, component.Tag.Stripped,
		existingCommit, currentCommit, existingRevision, component.Revision)
	return actionBumpRevision, ""
}

// decideForNewSpec discovers the partner repo's build files for the new tag,
// then diffs them against the highest same-major snapshot to choose between
// BUMP VERSION (snapshot matches) and GENERATE (no snapshot, or differs).
// New-spec branches always use revision 1.
func decideForNewSpec(component *workplan.WorkComponent) (pipelineAction, string) {
	log.Printf("─── [%s @ %s] Discover build files ───", component.Naming.SpecImageName, component.Tag.Stripped)
	log.Println("Purpose: Fetching partner-repo Dockerfile/Makefile for this tag")

	if err := partnerrepo.DiscoverBuildFiles(component); err != nil {
		log.Fatalf("❌ DiscoverBuildFiles failed: %v", err)
	}

	component.Revision = 1

	templateKey, found := semver.FindTemplateVersion(component)
	if !found {
		log.Printf("Result: no BuildFiles snapshot found -> action=GENERATE")
		log.Println()
		return actionGenerate, ""
	}

	templateDF := fetchSnapshot(pathcache.BuildDockerfilePath(component.Naming, templateKey))
	templateMF := fetchSnapshot(pathcache.BuildMakefilePath(component.Naming, templateKey))

	if buildFilesMatch(component, templateDF, templateMF) {
		log.Printf("Result: template=%s, BuildFiles match -> action=BUMP VERSION", templateKey)
		log.Println()
		return actionBumpVersion, templateKey
	}

	log.Printf("Result: template=%s, BuildFiles differ -> action=GENERATE", templateKey)
	log.Println()
	return actionGenerate, ""
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

// buildFilesMatch returns true when the freshly-fetched partner Dockerfile and
// Makefile (on component.BuildFiles.*.Source) byte-equal the template tag's files
// after trimming trailing newlines.
func buildFilesMatch(component *workplan.WorkComponent, templateDF, templateMF []byte) bool {
	freshDF := component.BuildFiles.Dockerfile.Source
	freshMF := component.BuildFiles.Makefile.Source

	if len(templateDF) == 0 && len(templateMF) == 0 {
		log.Printf("⚠️  Template tag has no Dockerfile/Makefile — forcing GENERATE")
		return false
	}

	if len(templateDF) > 0 {
		if len(freshDF) == 0 || !bytes.Equal(bytes.TrimRight(freshDF, "\n"), bytes.TrimRight(templateDF, "\n")) {
			log.Printf("Dockerfile changed for %s\n", component.Naming.SpecImageName)
			return false
		}
	}
	if len(templateMF) > 0 {
		if len(freshMF) == 0 || !bytes.Equal(bytes.TrimRight(freshMF, "\n"), bytes.TrimRight(templateMF, "\n")) {
			log.Printf("Makefile changed for %s\n", component.Naming.SpecImageName)
			return false
		}
	}

	log.Printf("✅ Build files unchanged for %s\n", component.Naming.SpecImageName)
	return true
}

// ─── Action dispatch ────────────────────────────────────────────────────────

func dispatchAction(action pipelineAction, templateKey string, component *workplan.WorkComponent) buildresult.BuildResult {
	switch action {
	case actionSkip:
		return buildresult.BuildResult{Outcome: buildresult.OutcomeSkipped}
	case actionBumpRevision:
		return runBumpRevision(component)
	case actionBumpVersion:
		return runBumpVersion(component, templateKey)
	case actionGenerate:
		return runGenerate(component)
	}
	return buildresult.BuildResult{Outcome: buildresult.OutcomeUnknown}
}

func runBumpVersion(component *workplan.WorkComponent, templateKey string) buildresult.BuildResult {
	log.Printf("─── [%s @ %s] Bump version ───", component.Naming.SpecImageName, component.Tag.Stripped)
	log.Println("Purpose: Copying template spec with updated commit hash (no content change)")

	specBytes, err := spec.BumpVersion(component, templateKey)
	if err != nil {
		log.Fatalf("❌ Version bump failed: %v", err)
	}

	return newSpecResult(component, buildresult.OutcomeBumpVersion, specBytes)
}

func runBumpRevision(component *workplan.WorkComponent) buildresult.BuildResult {
	log.Printf("─── [%s @ %s] Bump revision ───", component.Naming.SpecImageName, component.Tag.Stripped)
	log.Println("Purpose: Same version tag re-pushed with new commit — incrementing revision")

	specBytes, err := spec.BumpRevision(component)
	if err != nil {
		log.Fatalf("❌ Revision bump failed: %v", err)
	}

	return newSpecResult(component, buildresult.OutcomeBumpRevision, specBytes)
}

func runGenerate(component *workplan.WorkComponent) buildresult.BuildResult {
	log.Printf("─── [%s @ %s] Generate spec ───", component.Naming.SpecImageName, component.Tag.Stripped)
	log.Println("Purpose: Parsing Dockerfile/Makefile and generating full dalec spec from scratch")

	specBytes, _, err := spec.GenerateSpec(component)
	if err != nil {
		log.Printf("⚠️  Skipping %s @ %s: %v", component.Naming.SpecImageName, component.Tag.Full, err)
		return buildresult.BuildResult{Outcome: buildresult.OutcomeFailed}
	}

	return newSpecResult(component, buildresult.OutcomeGenerated, specBytes)
}

// ─── Helpers ────────────────────────────────────────────────────────────────

// newSpecResult packages encoded spec bytes into a buildresult.BuildResult.
func newSpecResult(component *workplan.WorkComponent, outcome buildresult.Outcome, specContent []byte) buildresult.BuildResult {
	log.Printf("✅ Spec ready: %s @ %s-%d", component.Naming.SpecImageName, component.Tag.Version, component.Revision)
	return buildresult.BuildResult{
		Outcome:     outcome,
		SpecContent: specContent,
	}
}
