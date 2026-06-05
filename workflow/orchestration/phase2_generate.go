package orchestration

import (
	"bytes"
	"log"

	"dalec-mapping/domain/buildresult"
	"dalec-mapping/domain/pathcache"
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
// license) are reached via component.Group.
func GenerateOne(component *workplan.WorkComponent) {
	logging.PrintComponentBanner(component)

	action, templateMinor := resolveAction(component)
	component.Result = dispatchAction(action, templateMinor, component)
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
//  3. Different version + template's BuildFiles match the new tag's source → bump version
//  4. Otherwise                                       → generate
//
// component.Revision is the NEXT revision to create (NextRevision = latest+1 when a
// prior revision exists, else 1). Therefore, when Revision > 1 the same-version
// case applies: a spec exists at Revision-1 and we must decide skip vs. bump-revision.
//
// When the returned action is actionBumpVersion, the second return value is
// the chosen template "<major>.<minor>" prefix (e.g. "1.6"). For every other
// action it is the empty string.
func resolveAction(component *workplan.WorkComponent) (pipelineAction, string) {
	if component.Revision > 1 {
		needsRevisionBump, err := spec.DetectRevisionBump(component)
		if err != nil {
			log.Fatalf("❌ DetectRevisionBump failed: %v", err)
		}
		if needsRevisionBump {
			return actionBumpRevision, ""
		}
		log.Printf("Skipping %s @ %s — spec already up to date\n", component.Naming.SpecImageName, component.Tag.Stripped)
		return actionSkip, ""
	}

	log.Printf("─── [%s @ %s] Discover build files ───", component.Naming.SpecImageName, component.Tag.Stripped)
	log.Println("Purpose: Fetching partner-repo Dockerfile/Makefile for this tag")

	if err := partnerrepo.DiscoverBuildFiles(component); err != nil {
		log.Fatalf("❌ DiscoverBuildFiles failed: %v", err)
	}

	templateMinor, found := semver.FindTemplateVersion(component)
	if !found {
		log.Printf("Result: no BuildFiles snapshot found -> action=GENERATE")
		log.Println()
		return actionGenerate, ""
	}

	templateDF := fetchSnapshot(pathcache.BuildDockerfilePath(component.Naming, templateMinor))
	templateMF := fetchSnapshot(pathcache.BuildMakefilePath(component.Naming, templateMinor))

	if buildFilesMatch(component, templateDF, templateMF) {
		log.Printf("Result: template minor=%s, BuildFiles match -> action=BUMP VERSION", templateMinor)
		log.Println()
		return actionBumpVersion, templateMinor
	}

	log.Printf("Result: template minor=%s, BuildFiles differ -> action=GENERATE", templateMinor)
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

func dispatchAction(action pipelineAction, templateMinor string, component *workplan.WorkComponent) buildresult.BuildResult {
	switch action {
	case actionSkip:
		return buildresult.BuildResult{Outcome: buildresult.OutcomeSkipped}
	case actionBumpRevision:
		return runBumpRevision(component)
	case actionBumpVersion:
		return runBumpVersion(component, templateMinor)
	case actionGenerate:
		return runGenerate(component)
	}
	return buildresult.BuildResult{Outcome: buildresult.OutcomeUnknown}
}

func runBumpVersion(component *workplan.WorkComponent, templateMinor string) buildresult.BuildResult {
	log.Printf("─── [%s @ %s] Bump version ───", component.Naming.SpecImageName, component.Tag.Stripped)
	log.Println("Purpose: Copying template spec with updated commit hash (no content change)")

	specBytes, err := spec.BumpVersion(component, templateMinor)
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
