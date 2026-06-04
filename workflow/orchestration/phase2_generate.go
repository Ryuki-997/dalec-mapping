package orchestration

import (
	"bytes"
	"fmt"
	"log"
	"strings"

	"dalec-mapping/domain/buildresult"
	"dalec-mapping/domain/tagcache"
	"dalec-mapping/domain/workplan"
	"dalec-mapping/workflow/foundations/logging"
	"dalec-mapping/workflow/infrastructure/specapi"
	"dalec-mapping/workflow/services/partnerrepo"
	"dalec-mapping/workflow/services/spec"
)

// ═══════════════════════════════════════════════════════════════════════════════
// Phase 2 — Generate
//
//   Input:  []workplan.WorkItemGroup
//   Output: none (mutates each item.Result in place)
//
//   Responsibilities:
//     - For each *workplan.WorkItem in every group, decide
//       skip/bump-revision/bump-version/generate
//     - Execute the chosen action (partnerrepo.DiscoverBuildFiles / spec.Bump* /
//       spec.GenerateSpec)
//     - Write the resulting BuildResult to item.Result. Identity fields
//       (Naming, Component, Tag) populated by Phase 1 are never modified;
//       BuildFiles is filled by the action sub-steps which take *WorkItem.
// ═══════════════════════════════════════════════════════════════════════════════

// ─── Public phase API ───────────────────────────────────────────────────────

// Generate runs Phase 2 across every item in the supplied groups.
// The groups hold []*WorkItem, so assigning to item.Result here is the
// single mutation Phase 2 makes; the group slice itself is untouched.
func Generate(groups []workplan.WorkItemGroup) {
	log.Println("═══ Phase 2: Generate ═══")
	for _, group := range groups {
		for _, item := range group.Items {
			GenerateOne(item)
		}
	}
}

// GenerateOne runs Phase 2 for a single *workplan.WorkItem and writes the
// outcome to item.Result. Exposed for testing and for callers that want
// per-item control.
func GenerateOne(item *workplan.WorkItem) {
	logging.PrintComponentBanner(item)

	action := resolveAction(item)
	item.Result = dispatchAction(action, item)
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
// tagSet.Revision is the NEXT revision to create (NextRevision = latest+1 when a
// prior revision exists, else 1). Therefore, when Revision > 1 the same-version
// case applies: a spec exists at Revision-1 and we must decide skip vs. bump-revision.
func resolveAction(item *workplan.WorkItem) pipelineAction {
	if item.Tag.Revision > 1 {
		needsRevisionBump, err := spec.DetectRevisionBump(item)
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
	log.Println("Purpose: Fetching partner-repo Dockerfile/Makefile for this tag")

	if err := partnerrepo.DiscoverBuildFiles(item); err != nil {
		log.Fatalf("❌ DiscoverBuildFiles failed: %v", err)
	}

	templatePath, templateFound := specapi.SpecRepoFindLatestMinorVersion(item.Naming, item.Tag.Stripped)
	if !templateFound {
		log.Printf("Result: templateFound=false -> action=GENERATE")
		log.Println()
		return actionGenerate
	}

	templateVersion, err := specapi.SpecRepoExtractTemplateVersion(templatePath, item.Naming)
	if err != nil {
		log.Fatalf("❌ SpecRepoExtractTemplateVersion failed: %v", err)
	}

	templateTag, err := deriveTemplateTag(item, templateVersion)
	if err != nil {
		log.Printf("⚠️  Template tag not in cache (%v) — forcing GENERATE", err)
		log.Println()
		return actionGenerate
	}

	templateDF, templateMF, err := partnerrepo.FetchBuildFilesAtTag(item, templateTag)
	if err != nil {
		log.Fatalf("❌ FetchBuildFilesAtTag(%s) failed: %v", templateTag, err)
	}

	if buildFilesMatch(item, templateDF, templateMF) {
		log.Printf("Result: template=%s @ %s, BuildFiles match -> action=BUMP VERSION", templatePath, templateTag)
		log.Println()
		return actionBumpVersion
	}

	log.Printf("Result: template=%s @ %s, BuildFiles differ -> action=GENERATE", templatePath, templateTag)
	log.Println()
	return actionGenerate
}

// buildFilesMatch returns true when the freshly-fetched partner Dockerfile and
// Makefile (on item.BuildFiles.*.Source) byte-equal the template tag's files
// after trimming trailing newlines.
func buildFilesMatch(item *workplan.WorkItem, templateDF, templateMF []byte) bool {
	freshDF := item.BuildFiles.Dockerfile.Source
	freshMF := item.BuildFiles.Makefile.Source

	if len(templateDF) == 0 && len(templateMF) == 0 {
		log.Printf("⚠️  Template tag has no Dockerfile/Makefile — forcing GENERATE")
		return false
	}

	if len(templateDF) > 0 {
		if len(freshDF) == 0 || !bytes.Equal(bytes.TrimRight(freshDF, "\n"), bytes.TrimRight(templateDF, "\n")) {
			log.Printf("Dockerfile changed for %s\n", item.Naming.SpecImageName)
			return false
		}
	}
	if len(templateMF) > 0 {
		if len(freshMF) == 0 || !bytes.Equal(bytes.TrimRight(freshMF, "\n"), bytes.TrimRight(templateMF, "\n")) {
			log.Printf("Makefile changed for %s\n", item.Naming.SpecImageName)
			return false
		}
	}

	log.Printf("✅ Build files unchanged for %s\n", item.Naming.SpecImageName)
	return true
}

// ─── Action dispatch ────────────────────────────────────────────────────────

func dispatchAction(action pipelineAction, item *workplan.WorkItem) buildresult.BuildResult {
	switch action {
	case actionSkip:
		return buildresult.BuildResult{Outcome: buildresult.OutcomeSkipped}
	case actionBumpRevision:
		return runBumpRevision(item)
	case actionBumpVersion:
		return runBumpVersion(item)
	case actionGenerate:
		return runGenerate(item)
	}
	return buildresult.BuildResult{Outcome: buildresult.OutcomeUnknown}
}

func runBumpVersion(item *workplan.WorkItem) buildresult.BuildResult {
	log.Printf("─── [%s @ %s] Bump version ───", item.Naming.SpecImageName, item.Tag.Stripped)
	log.Println("Purpose: Copying template spec with updated commit hash (no content change)")

	specBytes, err := spec.BumpVersion(item)
	if err != nil {
		log.Fatalf("❌ Version bump failed: %v", err)
	}

	return newSpecResult(item, buildresult.OutcomeBumpVersion, specBytes)
}

func runBumpRevision(item *workplan.WorkItem) buildresult.BuildResult {
	log.Printf("─── [%s @ %s] Bump revision ───", item.Naming.SpecImageName, item.Tag.Stripped)
	log.Println("Purpose: Same version tag re-pushed with new commit — incrementing revision")

	specBytes, err := spec.BumpRevision(item)
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
		return buildresult.BuildResult{Outcome: buildresult.OutcomeFailed}
	}

	return newSpecResult(item, buildresult.OutcomeGenerated, specBytes)
}

// ─── Helpers ────────────────────────────────────────────────────────────────

// deriveTemplateTag resolves the full repo tag for a different version of the
// same component, using the workitem's own tag as a reference for the repo's
// tag-prefix convention (e.g. "azure-ipam/v" vs "v"). targetVersion is the
// stripped semver with no "v" prefix (e.g. "0.4.0"). The candidate is looked
// up against tagcache.Cache so phase 2 only proceeds when the tag actually
// exists in the partner repo.
func deriveTemplateTag(item *workplan.WorkItem, targetVersion string) (string, error) {
	repoTags, ok := tagcache.Cache[item.Component.Repository]
	if !ok {
		return "", fmt.Errorf("no cached tags for repo %s", item.Component.Repository)
	}
	prefix := strings.TrimSuffix(item.Tag.Full, item.Tag.Stripped)
	candidate := prefix + "v" + targetVersion
	if _, ok := repoTags[candidate]; !ok {
		return "", fmt.Errorf("template tag %s not found in repo tags for %s", candidate, item.Component.Repository)
	}
	return candidate, nil
}

// newSpecResult packages encoded spec bytes into a buildresult.BuildResult.
func newSpecResult(item *workplan.WorkItem, outcome buildresult.Outcome, specContent []byte) buildresult.BuildResult {
	log.Printf("✅ Spec ready: %s @ %s-%d", item.Naming.SpecImageName, item.Tag.Version, item.Tag.Revision)
	return buildresult.BuildResult{
		Outcome:     outcome,
		SpecContent: specContent,
	}
}
