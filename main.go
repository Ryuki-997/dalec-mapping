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
//   Chunk 4 · ACTIONS     bumpVersion(), generateWork()
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
			actionLabel = "BUMP VERSION"
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

	action := resolveAction(onboard, tagSet, existingPaths)
	if action == actionSkip {
		return nil
	}
	return dispatchAction(action, onboard, tagSet, existingPaths)
}

// resolveAction is the single decision point for what happens to a (component, tag) pair.
// It checks — in order:
//  1. Same version already exists with matching commit → skip
//  2. Same version exists with different commit       → bump revision
//  3. Different version (no content change) + template exists → bump version
//  4. Otherwise                                       → generate
func resolveAction(onboard *onboarding.ComponentConfig, tagSet onboarding.TagSet, existingPaths map[string]bool) pipelineAction {
	// ── Check if this exact version already exists on remote ──
	componentState := onboarding.ComponentState{Onboard: onboard, Tag: tagSet}
	resolved := naming.Resolve(componentState)

	if existingPaths[resolved.SpecFilePath] {
		needsRevisionBump, err := workflow.DetectRevisionBump(existingPaths)
		if err != nil {
			log.Fatalf("❌ DetectRevisionBump failed: %v", err)
		}
		if needsRevisionBump {
			return actionBumpRevision
		}
		log.Printf("Skipping %s @ %s — spec already up to date\n", onboard.SpecImageName, tagSet.Stripped)
		return actionSkip
	}

	// ── New version: discover build files to decide generate vs bump version ──
	log.Printf("─── [%s @ %s] Step 3: Discover Build Files ───", onboard.SpecImageName, tagSet.Stripped)
	log.Println("Purpose: Checking Dockerfile/Makefile changes vs. existing siblings")

	contentChanged, err := workflow.DiscoverBuildFiles()
	if err != nil {
		log.Fatalf("❌ DiscoverBuildFiles failed: %v", err)
	}

	isFirstOnboard := onboard.DockerfileContent == nil && onboard.MakefileContent == nil

	// Content changed or first time → must generate from scratch
	if isFirstOnboard || contentChanged {
		log.Printf("Result: contentChanged=%v, firstOnboard=%v -> action=GENERATE", contentChanged, isFirstOnboard)
		log.Println()
		return actionGenerate
	}

	// Content unchanged → bump version if a template exists on remote
	_, templateFound := utils.SpecRepoFindLatestVersion(onboard.SpecDir(), onboard.SpecImageName, existingPaths)
	if !templateFound {
		log.Printf("No existing spec found for %s — falling back to generate", onboard.SpecImageName)
		log.Println()
		return actionGenerate
	}

	log.Printf("Result: contentChanged=%v, firstOnboard=%v -> action=BUMP VERSION", contentChanged, isFirstOnboard)
	log.Println()
	return actionBumpVersion
}

// dispatchAction executes the resolved pipeline action and returns the result.
func dispatchAction(action pipelineAction, onboard *onboarding.ComponentConfig, tagSet onboarding.TagSet, existingPaths map[string]bool) *workflow.ComponentSpec {
	switch action {
	case actionBumpRevision:
		return bumpRevision(onboard, tagSet)
	case actionBumpVersion:
		return bumpVersion(onboard, tagSet, existingPaths)
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
	actionSkip         pipelineAction = iota // Spec already up to date — no action needed
	actionBumpVersion                         // Copy template spec with new commit hash + version
	actionBumpRevision                       // Same version, new commit → increment revision
	actionGenerate                           // Generate spec → test → PR
)

// ─── Chunk 4 · ACTIONS ──────────────────────────────────────────────────────

func bumpVersion(onboard *onboarding.ComponentConfig, tagSet onboarding.TagSet, existingPaths map[string]bool) *workflow.ComponentSpec {
	log.Printf("─── [%s @ %s] Step 4: Bump Version ───", onboard.SpecImageName, tagSet.Stripped)
	log.Println("Purpose: Copying template spec with updated commit hash (no content change)")

	if err := workflow.BumpVersion(existingPaths); err != nil {
		log.Fatalf("❌ Version bump failed: %v", err)
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
