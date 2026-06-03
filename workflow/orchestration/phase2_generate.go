package orchestration

import (
	"log"

	"dalec-mapping/domain/buildresult"
	"dalec-mapping/domain/workplan"
	"dalec-mapping/workflow/foundations/logging"
	"dalec-mapping/workflow/infrastructure/specapi"
	"dalec-mapping/workflow/services/partnerrepo"
	"dalec-mapping/workflow/services/spec"
)

// ═══════════════════════════════════════════════════════════════════════════════
// Phase 2 — Generate
//
//   Input:  workplan.WorkPlan
//   Output: []buildresult.BuildResult (one per workplan.WorkItem; failures isolated)
//
//   Responsibilities:
//     - For each workplan.WorkItem, decide skip/bump-revision/bump-version/generate
//     - Execute the chosen action (partnerrepo.DiscoverBuildFiles / spec.Bump* / spec.GenerateSpec)
//     - Return a buildresult.BuildResult describing the outcome and (when applicable)
//       the resulting spec bytes
//
//   Each item is passed explicitly to its sub-steps as *workplan.WorkItem; the
//   sub-steps populate item.BuildFiles incrementally (discover → parse → fetch
//   repo metadata → extract). No ambient package globals.
// ═══════════════════════════════════════════════════════════════════════════════

// ─── Public phase API ───────────────────────────────────────────────────────

// Generate runs Phase 2 across every item in the workplan.WorkPlan. Always returns
// one buildresult.BuildResult per item (never nil).
func Generate(plan workplan.WorkPlan) []buildresult.BuildResult {
	log.Println("═══ Phase 2: Generate ═══")
	results := make([]buildresult.BuildResult, 0, len(plan.Items))
	for _, item := range plan.Items {
		results = append(results, GenerateOne(item, plan.ExistingPaths))
	}
	return results
}

// GenerateOne runs Phase 2 for a single workplan.WorkItem. Exposed for testing and
// for callers that want per-item control. Never returns nil.
func GenerateOne(item workplan.WorkItem, existingPaths map[string]bool) buildresult.BuildResult {
	logging.PrintComponentBanner(item)

	action := resolveAction(&item, existingPaths)
	return dispatchAction(action, &item, existingPaths)
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
// Order of checks:
//  1. Same version already exists with matching commit → skip
//  2. Same version exists with different commit       → bump revision
//  3. Different version (no content change) + template exists → bump version
//  4. Otherwise                                       → generate
//
// tagSet.Revision is the NEXT revision to create (NextRevision = latest+1 when a
// prior revision exists, else 1). Therefore, when Revision > 1 the same-version
// case applies: a spec exists at Revision-1 and we must decide skip vs. bump-revision.
func resolveAction(item *workplan.WorkItem, existingPaths map[string]bool) pipelineAction {
	if item.Tag.Revision > 1 {
		needsRevisionBump, err := spec.DetectRevisionBump(item, existingPaths)
		if err != nil {
			log.Fatalf("❌ DetectRevisionBump failed: %v", err)
		}
		if needsRevisionBump {
			return actionBumpRevision
		}
		log.Printf("Skipping %s @ %s — spec already up to date\n", item.Naming.SpecImageName, item.Tag.Stripped)
		return actionSkip
	}

	log.Printf("─── [%s @ %s] Discover build files ───", item.Naming.SpecImageName, item.Tag.Stripped)
	log.Println("Purpose: Checking Dockerfile/Makefile changes vs. existing siblings")

	contentChanged, err := partnerrepo.DiscoverBuildFiles(item)
	if err != nil {
		log.Fatalf("❌ DiscoverBuildFiles failed: %v", err)
	}

	_, templateFound := specapi.SpecRepoFindLatestMinorVersion(item.Naming, item.Tag.Stripped, existingPaths)
	if templateFound && !contentChanged {
		log.Printf("Result: templateFound=true, contentChanged=false -> action=BUMP VERSION")
		log.Println()
		return actionBumpVersion
	}

	log.Printf("Result: templateFound=%v, contentChanged=%v -> action=GENERATE", templateFound, contentChanged)
	log.Println()
	return actionGenerate
}

// ─── Action dispatch ────────────────────────────────────────────────────────

func dispatchAction(action pipelineAction, item *workplan.WorkItem, existingPaths map[string]bool) buildresult.BuildResult {
	switch action {
	case actionSkip:
		return buildresult.BuildResult{Item: *item, Outcome: buildresult.OutcomeSkipped}
	case actionBumpRevision:
		return runBumpRevision(item, existingPaths)
	case actionBumpVersion:
		return runBumpVersion(item, existingPaths)
	case actionGenerate:
		return runGenerate(item)
	}
	return buildresult.BuildResult{Item: *item, Outcome: buildresult.OutcomeUnknown}
}

func runBumpVersion(item *workplan.WorkItem, existingPaths map[string]bool) buildresult.BuildResult {
	log.Printf("─── [%s @ %s] Bump version ───", item.Naming.SpecImageName, item.Tag.Stripped)
	log.Println("Purpose: Copying template spec with updated commit hash (no content change)")

	specBytes, err := spec.BumpVersion(item, existingPaths)
	if err != nil {
		log.Fatalf("❌ Version bump failed: %v", err)
	}

	return newSpecResult(item, buildresult.OutcomeBumpVersion, specBytes)
}

func runBumpRevision(item *workplan.WorkItem, existingPaths map[string]bool) buildresult.BuildResult {
	log.Printf("─── [%s @ %s] Bump revision ───", item.Naming.SpecImageName, item.Tag.Stripped)
	log.Println("Purpose: Same version tag re-pushed with new commit — incrementing revision")

	specBytes, err := spec.BumpRevision(item, existingPaths)
	if err != nil {
		log.Fatalf("❌ Revision bump failed: %v", err)
	}

	return newSpecResult(item, buildresult.OutcomeBumpRevision, specBytes)
}

func runGenerate(item *workplan.WorkItem) buildresult.BuildResult {
	log.Printf("─── [%s @ %s] Generate spec ───", item.Naming.SpecImageName, item.Tag.Stripped)
	log.Println("Purpose: Parsing Dockerfile/Makefile and generating full dalec spec from scratch")

	specBytes, _, err := spec.GenerateSpec(item)
	if err != nil {
		log.Printf("⚠️  Skipping %s @ %s: %v", item.Naming.SpecImageName, item.Tag.Full, err)
		return buildresult.BuildResult{Item: *item, Outcome: buildresult.OutcomeFailed, Err: err}
	}

	return newSpecResult(item, buildresult.OutcomeGenerated, specBytes)
}

// ─── Helpers ────────────────────────────────────────────────────────────────

// newSpecResult packages encoded spec bytes into a buildresult.BuildResult.
func newSpecResult(item *workplan.WorkItem, outcome buildresult.Outcome, specContent []byte) buildresult.BuildResult {
	log.Printf("✅ Spec ready: %s @ %s-%d", item.Naming.SpecImageName, item.Tag.Version, item.Tag.Revision)
	return buildresult.BuildResult{
		Item:        *item,
		Outcome:     outcome,
		SpecContent: specContent,
	}
}
