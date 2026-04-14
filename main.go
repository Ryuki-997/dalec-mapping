package main

import (
	"dalec-mapping/domain/onboarding"
	"dalec-mapping/domain/repository"
	repoInfra "dalec-mapping/infrastructure/repository"
	"dalec-mapping/infrastructure/semver"
	"dalec-mapping/workflow"
	"flag"
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
	inputPath := flag.String("path", "", "Input path to search for onboarding files (e.g. containernetworking and containernetworking/azure-cns both work). Omit to fetch all under autospecs/")
	flag.Parse()

	loadEnv()

	onboardFiles, firstOnboardFlags, templateTags := fetchOnboardFiles(*inputPath)

	var specPaths []string
	for i, onboard := range onboardFiles {
		log.Printf("Onboard Documents: %v %v %v\n", onboard.Repository, onboard.SpecImageName, onboard.Tag)
		for _, fullTag := range onboard.Tag {
			if remotePath := processTag(&onboard, fullTag, firstOnboardFlags[i], templateTags[i]); remotePath != "" {
				specPaths = append(specPaths, remotePath)
			}
		}
	}

	log.Printf("specPaths=%s\n", strings.Join(specPaths, ","))
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

func fetchOnboardFiles(inputPath string) ([]onboarding.OnboardingInfo, []bool, []string) {
	var onboardFiles []onboarding.OnboardingInfo
	var firstOnboardFlags []bool
	var templateTags []string

	if err := workflow.FetchOnboardFiles(&onboardFiles, &firstOnboardFlags, &templateTags, inputPath); err != nil {
		log.Fatalf("❌ Failed to fetch onboard data: %v", err)
	}
	if len(onboardFiles) == 0 {
		log.Fatalf("No onboarding files found at path: %s", inputPath)
	}

	return onboardFiles, firstOnboardFlags, templateTags
}

// ─── Chunk 2 · PIPELINE ─────────────────────────────────────────────────────

// processTag runs the full pipeline for a single tag and returns the remote
// spec path if a new spec was generated, or empty string otherwise.
func processTag(onboard *onboarding.OnboardingInfo, fullTag string, isFirstOnboard bool, templateTag string) string {
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
		return bumpCommit(onboard, fullTag, tag, templateTag)
	case actionGenerate:
		remotePath, resolvedTargets, err := generateWork(onboard, fullTag)
		if err != nil {
			log.Printf("\u26a0\ufe0f  Skipping %s @ %s: %v\n", onboard.SpecImageName, fullTag, err)
			return ""
		}
		if onboard.ReviewMode == onboarding.AutoReview {
			testAndPush(onboard, remotePath, tag, resolvedTargets)
			return remotePath
		}
		testAndCreatePR(onboard, remotePath, tag, resolvedTargets)
		return "" // PR created — path not collected
	}

	return ""
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

func bumpCommit(onboard *onboarding.OnboardingInfo, fullTag, tag, templateTag string) string {
	log.Printf("🔄 Content unchanged for %s @ %s — bumping commit hash\n", onboard.SpecImageName, tag)
	if _, err := workflow.BumpCommit(onboard, fullTag, templateTag); err != nil {
		log.Fatalf("❌ Revision bump failed: %v", err)
	}
	if err := workflow.PushToRemote(onboard, tag, true); err != nil {
		log.Fatalf("❌ Push failed for %s @ %s: %v", onboard.SpecImageName, tag, err)
	}
	remotePath := semver.SpecFilePath(onboard.SpecRepository, onboard.SpecImageName, tag)
	log.Printf("✅ Revision bump pushed for %s @ %s\n", onboard.SpecImageName, tag)
	return remotePath
}

func generateWork(onboard *onboarding.OnboardingInfo, fullTag string) (string, []string, error) {
	log.Println("Dalec Spec Generator - Scheduled Job")
	log.Printf("Started at: %s", time.Now().Format(time.RFC3339))

	resolvedTargets, err := workflow.GenerateSpec(onboard, fullTag)
	if err != nil {
		return "", nil, err
	}

	log.Printf("✅ Spec created for %s @ %s", onboard.SpecImageName, fullTag)

	tag := semver.ToTag(fullTag)
	remotePath := semver.SpecFilePath(onboard.SpecRepository, onboard.SpecImageName, tag)
	return remotePath, resolvedTargets, nil
}

func testAndCreatePR(onboard *onboarding.OnboardingInfo, remotePath, tag string, resolvedTargets []string) {
	// Only run local docker-build tests for GitHub repos. ADO repos skip
	// testing here — the pipeline build itself serves as the test run.
	if !repoInfra.IsADORepo(onboard.Repository) {
		if err := workflow.TestImage(remotePath, onboard.SpecImageName, tag, resolvedTargets); err != nil {
			log.Fatalf("❌ Image test failed for %s @ %s: %v", onboard.SpecImageName, tag, err)
		}
	} else {
		log.Printf("⏭️  Skipping local image test for ADO repo %s @ %s — pipeline build will validate", onboard.SpecImageName, tag)
	}
	prURL, err := workflow.CreateSpecPR(onboard, tag, false)
	if err != nil {
		log.Fatalf("❌ PR creation failed for %s @ %s: %v", onboard.SpecImageName, tag, err)
	}
	log.Printf("✅ PR created for %s @ %s: %s\n", onboard.SpecImageName, tag, prURL)
}

func testAndPush(onboard *onboarding.OnboardingInfo, remotePath, tag string, resolvedTargets []string) {
	// Only run local docker-build tests for GitHub repos. ADO repos skip
	// testing here — the pipeline build itself serves as the test run.
	if !repoInfra.IsADORepo(onboard.Repository) {
		if err := workflow.TestImage(remotePath, onboard.SpecImageName, tag, resolvedTargets); err != nil {
			log.Fatalf("❌ Image test failed for %s @ %s: %v", onboard.SpecImageName, tag, err)
		}
	} else {
		log.Printf("⏭️  Skipping local image test for ADO repo %s @ %s — pipeline build will validate", onboard.SpecImageName, tag)
	}
	if err := workflow.PushToRemote(onboard, tag, false); err != nil {
		log.Fatalf("❌ Push failed for %s @ %s: %v", onboard.SpecImageName, tag, err)
	}
	log.Printf("✅ Spec pushed for %s @ %s\n", onboard.SpecImageName, tag)
}