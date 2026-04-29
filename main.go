package main

import (
	"dalec-mapping/domain/onboarding"
	"dalec-mapping/domain/repository"
	"dalec-mapping/infrastructure/semver"
	"dalec-mapping/patching"
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
//   Chunk 1 · ENTRY       main(), loadEnv(), fetchOnboardFiles()
//   Chunk 2 · PIPELINE    processTag(), decideAction()
//   Chunk 3 · ACTIONS     bumpCommit(), generateSpec(), testAndCreatePR()
// ═══════════════════════════════════════════════════════════════════════════════

// ─── Chunk 1 · ENTRY ────────────────────────────────────────────────────────

func main() {
	inputPath := flag.String("path", "", "Input path to search for onboarding files (e.g. containernetworking and containernetworking/azure-cns both work). Omit to fetch all under specs/")
	patchMode := flag.Bool("patch", false, "Run patching workflow: fetch MCR images and scan for vulnerabilities")
	flag.Parse()

	loadEnv()

	if *patchMode {
		runPatchWorkflow()
		return
	}

	onboardFiles, firstOnboardFlags, templateTags := fetchOnboardFiles(*inputPath)

	// prGroups collects PR entries keyed by group name (or component name for standalone).
	// Each PREntry becomes one PR containing all its components' files.
	prGroups := make(map[string]*workflow.PREntry)

	for i, onboard := range onboardFiles {
		log.Printf("Onboard Documents: %v %v %v\n", onboard.Repository, onboard.SpecImageName, onboard.Tag)
		for _, fullTag := range onboard.Tag {
			remotePath, comp := processTag(&onboard, fullTag, firstOnboardFlags[i], templateTags[i])
			if comp != nil {
				groupKey := onboard.SpecImageName // standalone → own PR
				groupName := ""
				if onboard.GroupName != "" {
					groupKey = onboard.GroupName // grouped → shared PR
					groupName = onboard.GroupName
				}
				if prGroups[groupKey] == nil {
					prGroups[groupKey] = &workflow.PREntry{GroupName: groupName}
				}
				comp.RemotePath = remotePath
				prGroups[groupKey].Components = append(prGroups[groupKey].Components, *comp)
			}
		}
	}

	// Create one PR per group (or standalone component).
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

func loadEnv() {
	wd, _ := os.Getwd()
	log.Printf("Working directory: %s", wd)

	if err := godotenv.Load(); err != nil {
		log.Printf("No .env file found: %v", err)
	}

	if tok := os.Getenv("GH_TOKEN"); tok != "" {
		log.Printf("🔑 GH_TOKEN is set (prefix: %s...)", tok[:min(10, len(tok))])
	} else {
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

// ─── Chunk 2 · PIPELINE ─────────────────────────────────────────────────────

// processTag runs the full pipeline for a single tag and returns the remote
// spec path (if pushed directly) and a PR entry (if a PR should be created).
func processTag(onboard *onboarding.ComponentConfig, fullTag string, isFirstOnboard bool, templateTag string) (string, *workflow.ComponentSpec) {
	tag := semver.ToTag(fullTag)
	log.Printf("▶ Running pipeline for %s @ %s\n", onboard.Repository, fullTag)

	repoInfo := &repository.RepoInfo{}
	contentChanged, err := workflow.DiscoverBuildFiles(onboard, repoInfo, fullTag)
	if err != nil {
		log.Fatalf("❌ DiscoverBuildFiles failed: %v", err)
	}

	action := decideAction(isFirstOnboard, contentChanged)

	switch action {
	case actionBumpCommit:
		return bumpCommit(onboard, fullTag, tag, templateTag), nil
	case actionGenerate:
		remotePath, specContent, err := generateWork(onboard, fullTag)
		if err != nil {
			log.Printf("\u26a0\ufe0f  Skipping %s @ %s: %v\n", onboard.SpecImageName, fullTag, err)
			return "", nil
		}
		if onboard.ReviewMode == onboarding.AutoReview {
			GitPush(onboard, remotePath, tag, nil)
			return remotePath, nil
		}
		// Defer PR creation — return component spec to be grouped.
		return remotePath, &workflow.ComponentSpec{
			Onboard:     onboard,
			Tag:         tag,
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

// ─── Chunk 3 · ACTIONS ──────────────────────────────────────────────────────

func bumpCommit(onboard *onboarding.ComponentConfig, fullTag, tag, templateTag string) string {
	log.Printf("🔄 Content unchanged for %s @ %s — bumping commit hash\n", onboard.SpecImageName, tag)
	if _, err := workflow.BumpCommit(onboard, fullTag, templateTag); err != nil {
		log.Fatalf("❌ Revision bump failed: %v", err)
	}
	if err := workflow.PushToRemote(onboard, tag, true); err != nil {
		log.Fatalf("❌ Push failed for %s @ %s: %v", onboard.SpecImageName, tag, err)
	}
	remotePath := semver.SpecFilePath(onboard.SpecDir(), onboard.SpecImageName, tag)
	log.Printf("✅ Revision bump pushed for %s @ %s\n", onboard.SpecImageName, tag)
	return remotePath
}

func generateWork(onboard *onboarding.ComponentConfig, fullTag string) (string, []byte, error) {
	log.Println("Dalec Spec Generator - Scheduled Job")
	log.Printf("Started at: %s", time.Now().Format(time.RFC3339))

	_, err := workflow.GenerateSpec(onboard, fullTag)
	if err != nil {
		return "", nil, err
	}

	log.Printf("✅ Spec created for %s @ %s", onboard.SpecImageName, fullTag)

	tag := semver.ToTag(fullTag)

	// // Test the generated spec by building and running the container image.
	// if err := workflow.TestImage(utils.SpecPath, onboard.SpecImageName, tag, resolvedTargets); err != nil {
	// 	return "", nil, fmt.Errorf("image test failed for %s @ %s: %w", onboard.SpecImageName, tag, err)
	// }

	specContent, err := os.ReadFile(utils.SpecPath)
	if err != nil {
		return "", nil, fmt.Errorf("failed to read generated spec: %w", err)
	}

	remotePath := semver.SpecFilePath(onboard.SpecDir(), onboard.SpecImageName, tag)
	return remotePath, specContent, nil
}

func GitPush(onboard *onboarding.ComponentConfig, remotePath, tag string, resolvedTargets []string) {
	if err := workflow.PushToRemote(onboard, tag, false); err != nil {
		log.Fatalf("❌ Push failed for %s @ %s: %v", onboard.SpecImageName, tag, err)
	}
	log.Printf("✅ Spec pushed for %s @ %s\n", onboard.SpecImageName, tag)
}

// ─── Chunk 4 · PATCHING ─────────────────────────────────────────────────────

func runPatchWorkflow() {
	log.Println("🩹 Running patching workflow — scanning ACR images for vulnerabilities")

	scanResults, err := patching.FetchAndScanACRImages()
	if err != nil {
		log.Fatalf("❌ Patching workflow failed: %v", err)
	}

	if len(scanResults) == 0 {
		log.Println("  ⏭️  No ACR images found to scan")
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

	log.Println("🩹 Patching workflow complete")
}