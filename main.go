package main

import (
	"context"
	"dalec-mapping/domain/onboarding"
	"dalec-mapping/domain/repository"
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
//   Chunk 3 · ACTIONS     bumpCommit(), generateSpec(), testAndPush(), notifyReviewers()
// ═══════════════════════════════════════════════════════════════════════════════

// ─── Chunk 1 · ENTRY ────────────────────────────────────────────────────────

func main() {
	inputPath := flag.String("path", "", "Input path to search for onboarding files (e.g. specs/containernetworking and specs/containernetworking/azure-cns both work). Omit to fetch all under specs/")
	flag.Parse()

	loadEnv()

	onboardFiles, firstOnboardFlags, templateTags := fetchOnboardFiles(*inputPath)

	var specPaths []string
	for i, onboard := range onboardFiles {
		log.Printf("Onboard Documents: %v\n", onboard)
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
		log.Printf("⚠️  .env file not found at %s/.env: %v", wd, err)
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

	action := decideAction(onboard.ReviewMode, isFirstOnboard, contentChanged)

	switch action {
	case actionNotify:
		notifyReviewers(onboard, tag, isFirstOnboard)
		return ""
	case actionBumpCommit:
		bumpCommit(onboard, fullTag, tag, templateTag)
		return ""
	case actionGenerate:
		remotePath, resolvedTargets := generateSpec(onboard, fullTag)
		testAndPush(onboard, remotePath, tag, resolvedTargets)
		return remotePath
	}

	return ""
}

type pipelineAction int

const (
	actionNotify     pipelineAction = iota // Notify reviewers, skip generation
	actionBumpCommit                       // Copy template spec with new commit hash
	actionGenerate                         // Full LLM generate → test → push
)

// decideAction maps the decision matrix to a pipeline action.
//
//	First time  + ManualReview             → notify
//	First time  + AutoReview               → generate
//	Re-onboard  + content unchanged        → bump commit
//	Re-onboard  + content changed + Manual → notify
//	Re-onboard  + content changed + Auto   → generate
func decideAction(mode onboarding.ReviewMode, isFirstOnboard, contentChanged bool) pipelineAction {
	if isFirstOnboard {
		if mode == onboarding.ManualReview {
			return actionNotify
		}
		return actionGenerate
	}

	if !contentChanged {
		return actionBumpCommit
	}

	if mode == onboarding.ManualReview {
		return actionNotify
	}
	return actionGenerate
}

// ─── Chunk 3 · ACTIONS ──────────────────────────────────────────────────────

func notifyReviewers(onboard *onboarding.OnboardingInfo, tag string, isFirstOnboard bool) {
	if isFirstOnboard {
		log.Printf("📋 First-time onboard (manual review) for %s @ %s — notifying reviewers\n", onboard.SpecImageName, tag)
	} else {
		log.Printf("📋 Content changed (manual review) for %s @ %s — notifying reviewers\n", onboard.SpecImageName, tag)
	}
	if err := workflow.NotifyOwners(onboard, tag, isFirstOnboard); err != nil {
		log.Printf("⚠️  Owner notification failed: %v", err)
	}
}

func bumpCommit(onboard *onboarding.OnboardingInfo, fullTag, tag, templateTag string) {
	log.Printf("🔄 Content unchanged for %s @ %s — bumping commit hash\n", onboard.SpecImageName, tag)
	if _, err := workflow.BumpCommit(onboard, fullTag, templateTag); err != nil {
		log.Fatalf("❌ Revision bump failed: %v", err)
	}
	if err := workflow.PushToRemote(onboard, tag); err != nil {
		log.Fatalf("❌ Push failed: %v", err)
	}
	log.Printf("✅ Revision bump pushed for %s @ %s\n", onboard.SpecImageName, tag)
}

func generateSpec(onboard *onboarding.OnboardingInfo, fullTag string) (string, []string) {
	log.Println("Dalec Spec Generator - Scheduled Job")
	log.Printf("Started at: %s", time.Now().Format(time.RFC3339))

	ctx := context.Background()

	agentResponse, err := workflow.ExtractBuildValues(ctx, onboard)
	if err != nil {
		log.Fatalf("❌ ExtractBuildValues failed: %v", err)
	}
	log.Printf("✅ Non-deterministic values populated and saved to result directory")

	resolvedTargets, err := workflow.GenerateSpec(onboard, agentResponse, fullTag)
	if err != nil {
		log.Fatalf("❌ GenerateSpec failed: %v", err)
	}

	log.Printf("✅ Spec created for %s @ %s", onboard.SpecImageName, fullTag)

	tag := semver.ToTag(fullTag)
	remotePath := semver.SpecFilePath(onboard.SpecRepository, onboard.SpecImageName, tag)
	return remotePath, resolvedTargets
}

func testAndPush(onboard *onboarding.OnboardingInfo, remotePath, tag string, resolvedTargets []string) {
	if err := workflow.TestImage(remotePath, onboard.SpecImageName, tag, resolvedTargets); err != nil {
		log.Fatalf("❌ Image test failed for %s @ %s: %v", onboard.SpecImageName, tag, err)
	}
	if err := workflow.PushToRemote(onboard, tag); err != nil {
		log.Fatalf("❌ Push failed: %v", err)
	}
}