package main

import (
	"dalec-mapping/domain/naming"
	"dalec-mapping/domain/onboarding"
	"dalec-mapping/patching"
	"dalec-mapping/pipeline"
	"dalec-mapping/utils"
	"dalec-mapping/workflow"
	"fmt"
	"log"
	"os"
)

// ═══════════════════════════════════════════════════════════════════════════════
// Main — Dalec Spec Pipeline
//
//   Chunk 1 · ENTRY       main(), parseFlags(), loadEnv(), fetchOnboardStates()
//   Chunk 2 · ORCHESTRATION processOnboardFiles(), submitPRs()
//   Chunk 3 · PIPELINE    processTag(), decideAction()
//   Chunk 4 · ACTIONS     bumpCommit(), generateWork()
//   Chunk 5 · PATCHING    runPatchWorkflow()
// ═══════════════════════════════════════════════════════════════════════════════

// ─── Chunk 1 · ENTRY ────────────────────────────────────────────────────────

func main() {
	inputPath, patchMode := workflow.ParseFlags()

	workflow.LoadEnv()

	if patchMode {
		runPatchWorkflow()
		return
	}

	componentStates, existingPaths := workflow.RunFetchOnboard(inputPath)
	states := workflow.RunResolveTagCache(componentStates, existingPaths)
	prGroups := processOnboardStates(states, existingPaths)
	log.Printf("Total PR groups to submit: %d", len(prGroups))
	// submitPRs(prGroups)
}



// ─── Chunk 2 · ORCHESTRATION ─────────────────────────────────────────────────

// processOnboardStates iterates all pre-expanded states, running the pipeline
// for each and collecting PR entries keyed by group name.
// A unique prID is generated per group key so each component+version+revision
// combination gets its own identifier.
func processOnboardStates(states []pipeline.State, existingPaths map[string]bool) map[string]*workflow.PREntry {
	prGroups := make(map[string]*workflow.PREntry)
	groupPRIDs := make(map[string]string)
	var actionLog []utils.ActionEntry

	for _, state := range states {
		onboard := state.Onboard

		comp := processTag(state, existingPaths)
		if comp == nil {
			actionLog = append(actionLog, utils.ActionEntry{
				Component: onboard.SpecImageName,
				Version:   fmt.Sprintf("%s-%d", state.Tag.Version, state.Tag.Revision),
				Action:    "SKIPPED",
			})
			continue
		}

		writeGenerated(onboard.SpecImageName, state.Tag.Stripped, comp.SpecContent)
		diffWithGolden(onboard.SpecImageName, state.Tag.Stripped, comp.SpecContent)

		actionLabel := "GENERATE"
		if comp.SpecOnly {
			actionLabel = "BUMP COMMIT"
		}
		actionLog = append(actionLog, utils.ActionEntry{
			Component: onboard.SpecImageName,
			Version:   fmt.Sprintf("%s-%d", state.Tag.Version, state.Tag.Revision),
			Action:    actionLabel,
		})

		groupName := onboard.SpecImageName
		if onboard.GroupName != "" {
			groupName = onboard.GroupName
		}
		groupKey := fmt.Sprintf("%s@%s", groupName, state.Tag.Stripped)
		if prGroups[groupKey] == nil {
			prGroups[groupKey] = &workflow.PREntry{GroupName: onboard.GroupName}
			groupPRIDs[groupKey] = naming.GeneratePRID()
			log.Printf("Assigned PR ID %s to group %s", groupPRIDs[groupKey], groupKey)
		}
		prGroups[groupKey].Components = append(prGroups[groupKey].Components, *comp)
	}

	// Resolve naming for each group now that per-group prIDs are assigned.
	resolveGroupNaming(prGroups, groupPRIDs)

	utils.PrintActionLog(actionLog)

	for groupKey, entry := range prGroups {
		log.Printf("Group: %s, Components: %d\n", groupKey, len(entry.Components))
	}

	return prGroups
}

// resolveGroupNaming computes Naming for every component in each PR group
// using the group's unique prID.
func resolveGroupNaming(prGroups map[string]*workflow.PREntry, groupPRIDs map[string]string) {
	for groupKey, entry := range prGroups {
		prID := groupPRIDs[groupKey]
		for componentIndex := range entry.Components {
			comp := &entry.Components[componentIndex]
			componentState := onboarding.ComponentState{
				Onboard: comp.Onboard,
				Tag:     onboarding.NewTagSet("", "", comp.Tag, comp.Revision),
			}
			comp.Naming = naming.Resolve(componentState).WithPRID(prID)
		}
	}
}

// submitPRs creates one PR per group (or standalone component) and logs results.
func submitPRs(prGroups map[string]*workflow.PREntry) {
	log.Println("═══ Step 7: Create Pull Requests ═══")
	log.Println("Purpose: Creating PRs for generated/bumped specs")
	log.Printf("  Groups to submit: %d", len(prGroups))

	type prResult struct {
		url     string
		files   []string
		created bool
	}
	var results []prResult

	for groupName, entry := range prGroups {
		prURL, created, err := workflow.CreatePR(*entry)
		if err != nil {
			log.Printf("❌ PR creation failed for %s: %v", groupName, err)
			continue
		}
		var specPaths []string
		for _, comp := range entry.Components {
			specPaths = append(specPaths, comp.Naming.SpecFilePath)
		}
		results = append(results, prResult{url: prURL, files: specPaths, created: created})
	}

	createdCount := 0
	skippedCount := 0
	for _, result := range results {
		if result.created {
			createdCount++
		} else {
			skippedCount++
		}
	}

	log.Printf("PR Summary (%d created, %d skipped — already open):", createdCount, skippedCount)
	for i, result := range results {
		label := "created"
		if !result.created {
			label = "existing"
		}
		log.Printf("  PR #%d [%s]: %s", i+1, label, result.url)
		for _, file := range result.files {
			log.Printf("    - %s", file)
		}
	}
	log.Println()
}

// ─── Chunk 3 · PIPELINE ─────────────────────────────────────────────────────

// processTag runs the full pipeline for a single tag and returns a
// ComponentSpec queued for PR creation, or nil if skipped.
// Naming is left unset — resolved later in processOnboardStates once
// each PR group has its own unique prID.
func processTag(state pipeline.State, existingPaths map[string]bool) *workflow.ComponentSpec {
	onboard := state.Onboard
	tagSet := state.Tag

	pipeline.Reset()
	pipeline.Current.Onboard = onboard
	pipeline.Current.Tag = tagSet

	utils.PrintComponentBanner(onboard.SpecImageName, tagSet.Stripped)

	if comp, handled := checkRevisionBump(onboard, tagSet, existingPaths); handled {
		return comp
	}

	action := resolveAction(onboard, tagSet)
	return dispatchAction(action, onboard, tagSet)
}

// checkRevisionBump detects whether this tag needs a revision bump or can be
// skipped entirely. Returns (comp, true) if the tag was handled, (nil, false)
// if the caller should continue to action resolution.
func checkRevisionBump(onboard *onboarding.ComponentConfig, tagSet onboarding.TagSet, existingPaths map[string]bool) (*workflow.ComponentSpec, bool) {
	needsRevisionBump, err := workflow.DetectRevisionBump(existingPaths)
	if err != nil {
		log.Fatalf("❌ DetectRevisionBump failed: %v", err)
	}
	if needsRevisionBump {
		return bumpRevision(onboard, tagSet), true
	}

	// DetectRevisionBump returns false when: (a) spec doesn't exist yet, or
	// (b) spec exists with matching commit. Check existingPaths to distinguish.
	componentState := onboarding.ComponentState{Onboard: onboard, Tag: tagSet}
	resolved := naming.Resolve(componentState)
	if existingPaths[resolved.SpecFilePath] {
		log.Printf("Skipping %s @ %s — spec already up to date\n", onboard.SpecImageName, tagSet.Stripped)
		return nil, true
	}

	return nil, false
}

// resolveAction discovers build files and determines which pipeline action to take.
func resolveAction(onboard *onboarding.ComponentConfig, tagSet onboarding.TagSet) pipelineAction {
	log.Printf("─── [%s @ %s] Step 3: Discover Build Files ───", onboard.SpecImageName, tagSet.Stripped)
	log.Println("Purpose: Checking Dockerfile/Makefile changes vs. existing siblings")

	contentChanged, err := workflow.DiscoverBuildFiles()
	if err != nil {
		log.Fatalf("❌ DiscoverBuildFiles failed: %v", err)
	}

	isFirstOnboard := onboard.DockerfileContent == nil && onboard.MakefileContent == nil
	action := decideAction(isFirstOnboard, contentChanged)

	// If bump-commit is chosen but there's no prior revision, there's nothing
	// to copy from — fall back to full generation.
	if action == actionBumpCommit && tagSet.Revision <= 1 {
		log.Printf("No prior revision (R0) exists for %s @ %s — falling back to generate", onboard.SpecImageName, tagSet.Stripped)
		action = actionGenerate
	}

	actionLabel := "GENERATE"
	if action == actionBumpCommit {
		actionLabel = "BUMP COMMIT"
	}
	log.Printf("Result: contentChanged=%v, firstOnboard=%v -> action=%s", contentChanged, isFirstOnboard, actionLabel)
	log.Println()

	return action
}

// dispatchAction executes the resolved pipeline action and returns the result.
func dispatchAction(action pipelineAction, onboard *onboarding.ComponentConfig, tagSet onboarding.TagSet) *workflow.ComponentSpec {
	switch action {
	case actionBumpCommit:
		return bumpCommit(onboard, tagSet)
	case actionGenerate:
		comp, err := generateWork(onboard, tagSet)
		if err != nil {
			log.Printf("⚠️  Skipping %s @ %s: %v", onboard.SpecImageName, tagSet.Full, err)
			return nil
		}
		return comp
	}
	return nil
}

type pipelineAction int

const (
	actionBumpCommit   pipelineAction = iota // Copy template spec with new commit hash + version
	actionBumpRevision                       // Same version, new commit → increment revision
	actionGenerate                           // Generate spec → test → PR
)

// decideAction maps the decision matrix to a pipeline action.
//
//	First time onboard                → generate (full pipeline)
//	Re-onboard + content changed      → generate (full pipeline)
//	Re-onboard + content unchanged    → bump commit (update tag/hash only)
func decideAction(isFirstOnboard, contentChanged bool) pipelineAction {
	if isFirstOnboard || contentChanged {
		return actionGenerate
	}
	return actionBumpCommit
}

// ─── Chunk 4 · ACTIONS ──────────────────────────────────────────────────────

func bumpCommit(onboard *onboarding.ComponentConfig, tagSet onboarding.TagSet) *workflow.ComponentSpec {
	log.Printf("─── [%s @ %s] Step 4: Bump Commit ───", onboard.SpecImageName, tagSet.Stripped)
	log.Println("Purpose: Copying template spec with updated commit hash (no content change)")

	if err := workflow.BumpCommit(); err != nil {
		log.Fatalf("❌ Commit bump failed: %v", err)
	}

	return readSpecResult(onboard, tagSet, true)
}

func bumpRevision(onboard *onboarding.ComponentConfig, tagSet onboarding.TagSet) *workflow.ComponentSpec {
	log.Printf("─── [%s @ %s] Step 4b: Bump Revision ───", onboard.SpecImageName, tagSet.Stripped)
	log.Println("Purpose: Same version tag re-pushed with new commit — incrementing revision")

	if err := workflow.BumpRevision(); err != nil {
		log.Fatalf("❌ Revision bump failed: %v", err)
	}

	return readSpecResult(onboard, tagSet, true)
}

func generateWork(onboard *onboarding.ComponentConfig, tagSet onboarding.TagSet) (*workflow.ComponentSpec, error) {
	log.Printf("─── [%s @ %s] Step 5: Generate Spec ───", onboard.SpecImageName, tagSet.Stripped)
	log.Println("Purpose: Parsing Dockerfile/Makefile and generating full dalec spec from scratch")

	if _, err := workflow.GenerateSpec(); err != nil {
		return nil, err
	}

	return readSpecResult(onboard, tagSet, false), nil
}

// readSpecResult reads the written spec file and packages it into a ComponentSpec.
func readSpecResult(onboard *onboarding.ComponentConfig, tagSet onboarding.TagSet, specOnly bool) *workflow.ComponentSpec {
	specContent, err := os.ReadFile(utils.SpecPath)
	if err != nil {
		log.Fatalf("❌ Failed to read spec for %s @ %s: %v", onboard.SpecImageName, tagSet.Stripped, err)
	}

	log.Printf("✅ Spec written: %s @ %s-%d", onboard.SpecImageName, tagSet.Version, tagSet.Revision)
	return &workflow.ComponentSpec{
		Onboard:     onboard,
		Tag:         tagSet.Stripped,
		Revision:    tagSet.Revision,
		SpecContent: specContent,
		SpecOnly:    specOnly,
	}
}

// ─── Chunk 5 · PATCHING ─────────────────────────────────────────────────────

func runPatchWorkflow() {
	log.Println("Running patching workflow — scanning ACR images for vulnerabilities")

	scanResults, err := patching.FetchAndScanACRImages()
	if err != nil {
		log.Fatalf("❌ Patching workflow failed: %v", err)
	}

	if len(scanResults) == 0 {
		log.Println("  No ACR images found to scan")
		return
	}

	// Summarize results
	for _, resultPath := range scanResults {
		total, high, critical, err := patching.ParseScanResults(resultPath)
		if err != nil {
			log.Printf("⚠️  Failed to parse %s: %v\n", resultPath, err)
			continue
		}
		if total == 0 {
			log.Printf("  ✅ %s: no vulnerabilities\n", resultPath)
		} else {
			log.Printf("  ⚠️  %s: %d total (%d high, %d critical)\n", resultPath, total, high, critical)
		}
	}

	log.Println("Patching workflow complete")
}
