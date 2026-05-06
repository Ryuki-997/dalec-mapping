package main

import (
	"dalec-mapping/domain/naming"
	"dalec-mapping/domain/onboarding"
	"dalec-mapping/patching"
	"dalec-mapping/pipeline"
	"dalec-mapping/utils"
	"dalec-mapping/workflow"
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/joho/godotenv"
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
	inputPath, patchMode := parseFlags()

	loadEnv()

	if patchMode {
		runPatchWorkflow()
		return
	}

	componentStates, existingPaths := fetchOnboardStates(inputPath)
	states := resolveTagCache(componentStates, existingPaths)
	prID := naming.GeneratePRID()
	log.Printf("Run ID: %s", prID)
	prGroups := processOnboardStates(states, prID)
	for groupKey, entry := range prGroups {
		log.Printf("Group: %s, Components: %d\n", groupKey, len(entry.Components))
	}
	// submitPRs(prGroups)
}

// parseFlags registers and parses CLI flags, returning the resolved values.
func parseFlags() (string, bool) {
	inputPath := flag.String("path", "", "Input path to search for onboarding files (e.g. containernetworking and containernetworking/azure-cns both work). Omit to fetch all under specs/")
	patchMode := flag.Bool("patch", false, "Run patching workflow: fetch MCR images and scan for vulnerabilities")
	flag.Parse()
	return *inputPath, *patchMode
}

func loadEnv() {
	wd, _ := os.Getwd()
	log.Printf("Working directory: %s", wd)

	if err := godotenv.Load(); err != nil {
		log.Printf("No .env file found: %v", err)
	}

	if tok := os.Getenv("GH_TOKEN"); tok == "" {
		log.Printf("⚠️  GH_TOKEN is not set — GitHub API calls will be unauthenticated")
	}
}

func fetchOnboardStates(inputPath string) ([]pipeline.State, map[string]bool) {
	log.Println("═══ Step 1: Fetch Onboard Files ═══")
	log.Println("Purpose: Fetching onboard configs and separating into component queue")
	log.Printf("Input path: %s", inputPath)

	states, existingPaths, err := workflow.FetchOnboardStates(inputPath)
	if err != nil {
		log.Fatalf("❌ Failed to fetch onboard data: %v", err)
	}

	if len(states) == 0 {
		log.Fatalf("❌ Potentially No onboarding files found at path: %s", inputPath)
	}

	log.Printf("Component queue (%d components):", len(states))
	for i, state := range states {
		log.Printf("  [%d] %-20s repo=%s", i+1, state.Onboard.SpecImageName, state.Onboard.Repository)
	}
	log.Println()

	return states, existingPaths
}

func resolveTagCache(componentStates []pipeline.State, existingPaths map[string]bool) []pipeline.State {
	log.Println("═══ Step 2: Resolve Tag Cache ═══")
	log.Println("Purpose: Building global tag-to-commit cache and resolving actionable tags per component")

	states, err := workflow.ResolveTagCache(componentStates, existingPaths)
	if err != nil {
		log.Fatalf("❌ Failed to resolve tag cache: %v", err)
	}

	if len(states) == 0 {
		log.Fatalf("❌ No actionable tags found for any component")
	}

	// Group tags by component for display
	tagsByComponent := make(map[string][]string)
	for _, state := range states {
		name := state.Onboard.SpecImageName
		tagsByComponent[name] = append(tagsByComponent[name], fmt.Sprintf("%s (R%d)", state.Tag.Stripped, state.Tag.Revision))
	}
	log.Printf("Tag cache (%d tags across %d components):", len(states), len(tagsByComponent))
	for component, tags := range tagsByComponent {
		log.Printf("  %s:", component)
		for _, tag := range tags {
			log.Printf("    %s", tag)
		}
	}
	log.Println()

	return states
}

// ─── Chunk 2 · ORCHESTRATION ─────────────────────────────────────────────────

// actionEntry records what happened to a single component for the action log.
type actionEntry struct {
	Component string
	Version   string
	Action    string
}

// processOnboardStates iterates all pre-expanded states, running the pipeline
// for each and collecting PR entries keyed by group name.
func processOnboardStates(states []pipeline.State, prID string) map[string]*workflow.PREntry {
	prGroups := make(map[string]*workflow.PREntry)
	var actionLog []actionEntry

	for _, state := range states {
		onboard := state.Onboard

		comp := processTag(state, prID)
		if comp == nil {
			actionLog = append(actionLog, actionEntry{
				Component: onboard.SpecImageName,
				Version:   fmt.Sprintf("%s-%d", state.Tag.Version, state.Tag.Revision),
				Action:    "SKIPPED",
			})
			continue
		}

		actionLabel := "GENERATE"
		if comp.SpecOnly {
			actionLabel = "BUMP COMMIT"
		}
		actionLog = append(actionLog, actionEntry{
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
		}
		prGroups[groupKey].Components = append(prGroups[groupKey].Components, *comp)
	}

	printActionLog(actionLog)
	return prGroups
}

// printActionLog outputs a summary of all component actions taken during the run.
func printActionLog(entries []actionEntry) {
	log.Println()
	log.Println("═══ Action Log ═══")
	for _, entry := range entries {
		log.Printf("  %-12s %s @ %s", entry.Action, entry.Component, entry.Version)
	}
	log.Println()
}

// printComponentBanner prints a prominent box banner for a component being processed.
func printComponentBanner(component, tag string) {
	label := fmt.Sprintf("  %s @ %s", component, tag)
	width := len(label) + 4
	if width < 60 {
		width = 60
	}
	top := "╔" + repeatChar('═', width) + "╗"
	padded := fmt.Sprintf("║  %-*s  ║", width-4, fmt.Sprintf("%s @ %s", component, tag))
	bottom := "╚" + repeatChar('═', width) + "╝"

	log.Println()
	log.Println(top)
	log.Println(padded)
	log.Println(bottom)
}

// repeatChar returns a string of the given rune repeated n times.
func repeatChar(ch rune, count int) string {
	result := make([]rune, count)
	for i := range result {
		result[i] = ch
	}
	return string(result)
}

// submitPRs creates one PR per group (or standalone component) and logs results.
func submitPRs(prGroups map[string]*workflow.PREntry) {
	log.Println("═══ Step 7: Create Pull Requests ═══")
	log.Println("Purpose: Creating PRs for generated/bumped specs")
	log.Printf("  Groups to submit: %d", len(prGroups))

	type prResult struct {
		url   string
		files []string
	}
	var results []prResult

	for groupName, entry := range prGroups {
		prURL, err := workflow.CreatePR(*entry)
		if err != nil {
			log.Printf("❌ PR creation failed for %s: %v", groupName, err)
			continue
		}
		var specPaths []string
		for _, comp := range entry.Components {
			specPaths = append(specPaths, comp.Naming.SpecFilePath)
		}
		results = append(results, prResult{url: prURL, files: specPaths})
	}

	log.Printf("PR Summary (%d PRs created):", len(results))
	for i, result := range results {
		log.Printf("  PR #%d: %s", i+1, result.url)
		for _, file := range result.files {
			log.Printf("    - %s", file)
		}
	}
	log.Println()
}

// ─── Chunk 3 · PIPELINE ─────────────────────────────────────────────────────

// processTag runs the full pipeline for a single tag and returns a
// ComponentSpec queued for PR creation, or nil if skipped.
func processTag(state pipeline.State, prID string) *workflow.ComponentSpec {
	onboard := state.Onboard
	tagSet := state.Tag

	pipeline.Reset()
	pipeline.Current.Onboard = onboard
	pipeline.Current.Tag = tagSet

	printComponentBanner(onboard.SpecImageName, tagSet.Stripped)

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

	componentNaming := naming.Resolve([]onboarding.ComponentState{{Onboard: onboard, Tag: tagSet}}, prID)

	switch action {
	case actionBumpCommit:
		return bumpCommit(onboard, tagSet, componentNaming)
	case actionGenerate:
		comp, err := generateWork(onboard, tagSet, componentNaming)
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
	actionBumpCommit pipelineAction = iota // Copy template spec with new commit hash
	actionGenerate                         // Generate spec → test → PR
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

func bumpCommit(onboard *onboarding.ComponentConfig, tagSet onboarding.TagSet, componentNaming naming.Naming) *workflow.ComponentSpec {
	log.Printf("─── [%s @ %s] Step 4: Bump Commit ───", onboard.SpecImageName, tagSet.Stripped)
	log.Println("Purpose: Copying template spec with updated commit hash (no content change)")

	templateRevision := tagSet.Revision - 1

	if err := workflow.BumpCommit(tagSet.Full, templateRevision); err != nil {
		log.Fatalf("❌ Revision bump failed: %v", err)
	}

	specContent, err := os.ReadFile(utils.SpecPath)
	if err != nil {
		log.Fatalf("❌ Failed to read bumped spec for %s @ %s: %v", onboard.SpecImageName, tagSet.Stripped, err)
	}

	log.Printf("✅ Bump commit complete: %s", componentNaming.SpecFilePath)
	return &workflow.ComponentSpec{
		Onboard:     onboard,
		Tag:         tagSet.Stripped,
		Revision:    tagSet.Revision,
		SpecContent: specContent,
		SpecOnly:    true,
		Naming:      componentNaming,
	}
}

func generateWork(onboard *onboarding.ComponentConfig, tagSet onboarding.TagSet, componentNaming naming.Naming) (*workflow.ComponentSpec, error) {
	log.Printf("─── [%s @ %s] Step 5: Generate Spec ───", onboard.SpecImageName, tagSet.Stripped)
	log.Println("Purpose: Parsing Dockerfile/Makefile and generating full dalec spec from scratch")

	_, err := workflow.GenerateSpec()
	if err != nil {
		return nil, err
	}

	// // Test the generated spec by building and running the container image.
	// if err := workflow.TestImage(utils.SpecPath, onboard.SpecImageName, tagSet.Stripped, resolvedTargets); err != nil {
	// 	return nil, fmt.Errorf("image test failed for %s @ %s: %w", onboard.SpecImageName, tagSet.Stripped, err)
	// }

	specContent, err := os.ReadFile(utils.SpecPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read generated spec: %w", err)
	}

	log.Printf("✅ Spec generated: %s", componentNaming.SpecFilePath)
	return &workflow.ComponentSpec{
		Onboard:     onboard,
		Tag:         tagSet.Stripped,
		Revision:    tagSet.Revision,
		SpecContent: specContent,
		SpecOnly:    false,
		Naming:      componentNaming,
	}, nil
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
