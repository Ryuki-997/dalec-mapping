package main

import (
	"dalec-mapping/domain/onboarding"
	"dalec-mapping/infrastructure/semver"
	"dalec-mapping/patching"
	"dalec-mapping/pipeline"
	"dalec-mapping/utils"
	"dalec-mapping/workflow"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

// ═══════════════════════════════════════════════════════════════════════════════
// Main — Dalec Spec Pipeline
//
//   Chunk 1 · ENTRY       main(), parseFlags(), loadEnv(), fetchOnboardFiles()
//   Chunk 2 · ORCHESTRATION processOnboardFiles(), submitPRs()
//   Chunk 3 · PIPELINE    processTag(), decideAction()
//   Chunk 4 · ACTIONS     bumpCommit(), generateWork(), GitPush()
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

	onboardFiles, firstOnboardFlags, templateTags := fetchOnboardFiles(inputPath)
	prGroups := processOnboardFiles(onboardFiles, firstOnboardFlags, templateTags)
	log.Printf("Pipeline complete for all components and tags — preparing PRs\n")
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

func fetchOnboardFiles(inputPath string) ([]onboarding.ComponentConfig, []bool, []string) {
	var onboardFiles []onboarding.ComponentConfig
	var firstOnboardFlags []bool
	var templateTags []string

	if err := workflow.FetchOnboardFiles(&onboardFiles, &firstOnboardFlags, &templateTags, inputPath); err != nil {
		log.Fatalf("❌ Failed to fetch onboard data: %v", err)
	}

	if len(onboardFiles) == 0 {
		log.Fatalf("❌ Potentially No onboarding files found at path: %s", inputPath)
	}

	return onboardFiles, firstOnboardFlags, templateTags
}

// ─── Chunk 2 · ORCHESTRATION ─────────────────────────────────────────────────

// processOnboardFiles iterates all onboard configs and their tags, running the
// pipeline for each and collecting PR entries keyed by group name.
func processOnboardFiles(onboardFiles []onboarding.ComponentConfig, firstOnboardFlags []bool, templateTags []string) map[string]*workflow.PREntry {
	prGroups := make(map[string]*workflow.PREntry)

	for i, onboard := range onboardFiles {
		log.Printf("Onboard Documents: %v %v %v\n", onboard.Repository, onboard.SpecImageName, onboard.ResolvedTags)
		for _, tagSet := range onboard.ResolvedTags {
			remotePath, comp := processTag(&onboard, tagSet, firstOnboardFlags[i], templateTags[i])
			if comp == nil {
				continue
			}

			groupKey := onboard.SpecImageName
			groupName := ""
			if onboard.GroupName != "" {
				groupKey = onboard.GroupName
				groupName = onboard.GroupName
			}
			if prGroups[groupKey] == nil {
				prGroups[groupKey] = &workflow.PREntry{GroupName: groupName}
			}
			comp.RemotePath = remotePath
			prGroups[groupKey].Components = append(prGroups[groupKey].Components, *comp)
		}
	}

	return prGroups
}

// submitPRs creates one PR per group (or standalone component) and logs results.
func submitPRs(prGroups map[string]*workflow.PREntry) {
	for groupName, entry := range prGroups {
		prURL, err := workflow.CreatePR(*entry)
		if err != nil {
			log.Printf("❌ PR creation failed for %s: %v", groupName, err)
			continue
		}
		var specPaths []string
		for _, comp := range entry.Components {
			if comp.RemotePath != "" {
				specPaths = append(specPaths, comp.RemotePath)
			}
		}
		log.Printf("✅ PR created for %s: %s specPaths=%s\n", groupName, prURL, strings.Join(specPaths, ","))
	}
}

// ─── Chunk 3 · PIPELINE ─────────────────────────────────────────────────────

// processTag runs the full pipeline for a single tag and returns the remote
// spec path (if pushed directly) and a PR entry (if a PR should be created).
func processTag(onboard *onboarding.ComponentConfig, tagSet onboarding.TagSet, isFirstOnboard bool, templateTag string) (string, *workflow.ComponentSpec) {
	log.Printf("Running pipeline for %s @ %s (R%d)\n", onboard.Repository, tagSet.Full, tagSet.Revision)

	pipeline.Reset()
	pipeline.Current.Onboard = onboard
	pipeline.Current.Tag = tagSet

	contentChanged, err := workflow.DiscoverBuildFiles()
	if err != nil {
		log.Fatalf("❌ DiscoverBuildFiles failed: %v", err)
	}

	action := decideAction(isFirstOnboard, contentChanged)

	// If bump-commit is chosen but there's no prior revision and no template tag,
	// there's nothing to copy from — fall back to full generation.
	if action == actionBumpCommit && tagSet.Revision <= 1 && templateTag == "" {
		log.Printf("No prior revision (R0) exists for %s @ %s and no template tag — falling back to generate\n", onboard.SpecImageName, tagSet.Stripped)
		action = actionGenerate
	}

	switch action {
	case actionBumpCommit:
		return bumpCommit(onboard, tagSet, templateTag)
	case actionGenerate:
		remotePath, specContent, err := generateWork(onboard, tagSet)
		if err != nil {
			log.Printf("⚠️  Skipping %s @ %s: %v\n", onboard.SpecImageName, tagSet.Full, err)
			return "", nil
		}
		if onboard.ReviewMode == onboarding.AutoReview {
			GitPush(onboard, remotePath, tagSet.Stripped, nil)
			return remotePath, nil
		}
		// Defer PR creation — return component spec to be grouped.
		return remotePath, &workflow.ComponentSpec{
			Onboard:     onboard,
			Tag:         tagSet.Stripped,
			SpecContent: specContent,
			SpecOnly:    false,
		}
	}

	return "", nil
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
//	Re-onboard + content unchanged    → bump commit (update tag/hash, direct push)
//
// ReviewMode determines delivery: ManualReview → PR, AutoReview → direct push.
func decideAction(isFirstOnboard, contentChanged bool) pipelineAction {
	if isFirstOnboard || contentChanged {
		return actionGenerate
	}
	return actionBumpCommit
}

// ─── Chunk 4 · ACTIONS ──────────────────────────────────────────────────────

func bumpCommit(onboard *onboarding.ComponentConfig, tagSet onboarding.TagSet, templateTag string) (string, *workflow.ComponentSpec) {
	log.Printf("Content unchanged for %s @ %s — bumping commit hash\n", onboard.SpecImageName, tagSet.Stripped)

	// Template is the previous revision of the same tag (or of the templateTag if provided)
	templateRevision := tagSet.Revision - 1
	effectiveTemplateTag := tagSet.Full
	if templateTag != "" {
		effectiveTemplateTag = templateTag
	}

	if _, err := workflow.BumpCommit(effectiveTemplateTag, templateRevision); err != nil {
		log.Fatalf("❌ Revision bump failed: %v", err)
	}

	specContent, err := os.ReadFile(utils.SpecPath)
	if err != nil {
		log.Fatalf("❌ Failed to read bumped spec for %s @ %s: %v", onboard.SpecImageName, tagSet.Stripped, err)
	}

	remotePath := semver.SpecFilePath(onboard.SpecDir(), onboard.SpecImageName, tagSet.Stripped, tagSet.Revision)
	log.Printf("✅ Revision bump complete for %s @ %s R%d — queued for PR\n", onboard.SpecImageName, tagSet.Stripped, tagSet.Revision)
	return remotePath, &workflow.ComponentSpec{
		Onboard:     onboard,
		Tag:         tagSet.Stripped,
		SpecContent: specContent,
		SpecOnly:    true,
	}
}

func generateWork(onboard *onboarding.ComponentConfig, tagSet onboarding.TagSet) (string, []byte, error) {
	log.Println("Dalec Spec Generator - Scheduled Job")
	log.Printf("Started at: %s", time.Now().Format(time.RFC3339))

	_, err := workflow.GenerateSpec()
	if err != nil {
		return "", nil, err
	}

	log.Printf("✅ Spec created for %s @ %s", onboard.SpecImageName, tagSet.Full)

	// // Test the generated spec by building and running the container image.
	// if err := workflow.TestImage(utils.SpecPath, onboard.SpecImageName, tagSet.Stripped, resolvedTargets); err != nil {
	// 	return "", nil, fmt.Errorf("image test failed for %s @ %s: %w", onboard.SpecImageName, tagSet.Stripped, err)
	// }

	specContent, err := os.ReadFile(utils.SpecPath)
	if err != nil {
		return "", nil, fmt.Errorf("failed to read generated spec: %w", err)
	}

	remotePath := semver.SpecFilePath(onboard.SpecDir(), onboard.SpecImageName, tagSet.Stripped, tagSet.Revision)
	return remotePath, specContent, nil
}

func GitPush(onboard *onboarding.ComponentConfig, remotePath, tag string, resolvedTargets []string) {
	if err := workflow.PushToRemote(tag, false); err != nil {
		log.Fatalf("❌ Push failed for %s @ %s: %v", onboard.SpecImageName, tag, err)
	}
	log.Printf("✅ Spec pushed for %s @ %s\n", onboard.SpecImageName, tag)
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
