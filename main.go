package main

import (
	"context"
	"dalec-mapping/domain/onboarding"
	"dalec-mapping/domain/repository"
	"dalec-mapping/infrastructure/semver"
	"dalec-mapping/workflow"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

func main() {
	// Parse command line flags
	inputPath := flag.String("path", "", "Input path to search for onboarding files (e.g. specs/containernetworking and specs/containernetworking/azure-cns both work. It is the difference between building all components vs one). Omit to fetch all under specs/")
	flag.Parse()

	// Step 0: Load environment variables
	wd, _ := os.Getwd()
	log.Printf("Working directory: %s", wd)

	err := godotenv.Load()
	if err != nil {
		log.Printf("⚠️  .env file not found at %s/.env: %v", wd, err)
	}
	
	// Step 1: Fetch onboarding files from the onboard repo
	onboardFiles := []onboarding.OnboardingInfo{}
	err = workflow.FetchOnboardFiles(&onboardFiles, *inputPath)
	if err != nil {
		log.Fatalf("❌ Failed to fetch onboard data: %v", err)
	}

	if len(onboardFiles) == 0 {
		log.Fatalf("No onboarding files found at path: %s", *inputPath)
	}

	shellVar := []string{}
	for _, onboard := range onboardFiles {
		log.Printf("Onboard Documents: %v\n", onboard)
		for _, fullTag := range onboard.Tag {

			tag := semver.ToTag(fullTag)
			log.Printf("▶ Running pipeline for %s @ %s\n", onboard.Repository, fullTag)

			// Step 2: DiscoverBuildFiles — also sets onboard.Mode based on sibling diff.
			repoInfo := &repository.RepoInfo{}
			if err := workflow.DiscoverBuildFiles(&onboard, repoInfo, fullTag); err != nil {
				log.Fatalf("❌ DiscoverBuildFiles failed: %v", err)
			}

			// Step 3: Dockerfile/Makefile unchanged since last onboard? If yes, skip straight to BumpCommit with no LLM or generation.
			if onboard.Mode == onboarding.CommitBump {
				_, err := workflow.BumpCommit(&onboard, fullTag)
				if err != nil {
					log.Fatalf("❌ Revision bump failed: %v", err)
				}
				
				if err := workflow.PushToRemote(&onboard, tag); err != nil {
					log.Fatalf("❌ Push failed: %v", err)
				}
				log.Printf("✅ Revision bump pushed for %s @ %s\n", onboard.SpecImageName, tag)
				continue
			}

			// Steps 4-5: Full pipeline: LLM populate → generate → test → push.
			remotePath, resolvedTargets := generateSpec(&onboard, fullTag)
			shellVar = append(shellVar, remotePath)

			testAndPush(&onboard, remotePath, tag, fullTag, resolvedTargets)

			// Step 8: Notify owners after push.
			if err := workflow.NotifyOwners(&onboard, tag); err != nil {
				log.Printf("⚠️  Owner notification failed: %v", err)
			}
		}
	}

	log.Printf("specPaths=%s\n", strings.Join(shellVar, ","))
}

func generateSpec(onboard *onboarding.OnboardingInfo, fullTag string) (string, []string) {
	log.Println("Dalec Spec Generator - Scheduled Job")
	log.Printf("Started at: %s", time.Now().Format(time.RFC3339))
	log.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")

	ctx := context.Background()

	// Step 4: ExtractBuildValues — populate non-deterministic fields using LLM
	agentResponse, err := workflow.ExtractBuildValues(ctx, onboard)
	if err != nil {
		log.Fatalf("❌ Step 4 failed: %v", err)
		os.Exit(1)
	} 

	log.Printf("✅ Non-deterministic values populated and saved to result directory")

	// Step 5: GenerateSpec — create DALEC spec
	resolvedTargets, err := workflow.GenerateSpec(onboard, agentResponse, fullTag)
	if err != nil {
		log.Fatalf("❌ Step 5 failed: %v", err)
	}

	log.Println("\n━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	log.Printf("✅ Generation Complete at %s", time.Now().Format(time.RFC3339))

	log.Printf("✅ Spec created successfully for repository %s @ %s", onboard.Repository, onboard.Tag)

	tag := semver.ToTag(fullTag)
	var specPath string
	if onboard.SpecRepository == "" {
		specPath = fmt.Sprintf("specs/%s/%s-%s-specfile.yml", onboard.SpecImageName, onboard.SpecImageName, tag)
	} else {
		specPath = fmt.Sprintf("specs/%s/%s/%s-%s-specfile.yml", onboard.SpecRepository, onboard.SpecImageName, onboard.SpecImageName, tag)
	}
	return specPath, resolvedTargets
}

func testAndPush(onboard *onboarding.OnboardingInfo, remotePath, tag, fullTag string, resolvedTargets []string) {
	// Step 7: TestImage — build, run, and test the image before pushing
	if err := workflow.TestImage(remotePath, onboard.SpecImageName, tag, resolvedTargets); err != nil {
		log.Fatalf("❌ Image test failed for %s @ %s: %v", onboard.SpecImageName, tag, err)
	}

	// Step 6: PushToRemote — push to remote repository (only after image test passes)
	if err := workflow.PushToRemote(onboard, tag); err != nil {
		log.Fatalf("❌ Push failed: %v", err)
	}
}