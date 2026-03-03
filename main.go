package main

import (
	"context"
	"dalec-mapping/domain/llm"
	"dalec-mapping/domain/onboarding"
	"dalec-mapping/domain/repository"
	"dalec-mapping/workflow"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

func main() {
	wd, _ := os.Getwd()
	log.Printf("Working directory: %s", wd)
	
	err := godotenv.Load()
	if err != nil {
		log.Printf("⚠️  .env file not found at %s/.env: %v", wd, err)
		os.Exit(1)
	} 

	onboardFiles := []onboarding.OnboardingInfo{}

	err = workflow.FetchOnboardFiles(&onboardFiles)
	if err != nil {
		log.Fatalf("❌ Failed to fetch onboard data: %v", err)
	}

	shellVar := []string{}
	for _, onboard := range onboardFiles {
		fmt.Printf("Onboard Documents: %v\n", onboard)
		for _, tag := range onboard.Tag {
			fmt.Printf("▶ Running pipeline for %s @ %s\n", onboard.Repository, tag)
			remotePath, resolvedTargets := generateSpec(&onboard, tag)
			shellVar = append(shellVar, remotePath)

			// Step 6: build, run, and test the image
			// if err := workflow.ImageTest(remotePath, onboard.SpecImageName, tag, resolvedTargets); err != nil {
			// 	log.Fatalf("❌ Image test failed for %s @ %s: %v", onboard.SpecImageName, tag, err)
			// }
			_ = resolvedTargets
		}
	}

	fmt.Printf("specPaths=%s\n", strings.Join(shellVar, ","))

	// test()
}

func generateSpec(onboard *onboarding.OnboardingInfo, tag string) (string, []string) {
	log.Println("Dalec Spec Generator - Scheduled Job")
	log.Printf("Started at: %s", time.Now().Format(time.RFC3339))
	log.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")

	// Step 1: Discover build files
	fileContents := &llm.InstructionContents{
		Dockerfile: []byte{},
		Makefile:   []byte{},
	}
	repositoryInfo := &repository.RepoInfo{}
	log.Println("\n=== Step 1: Discover Build Files ===")
	err := workflow.Discover(onboard, fileContents, repositoryInfo, tag)
	if err != nil {
		log.Fatalf("❌ Step 1 failed: %v", err)
	}

	ctx := context.Background()

	// Step 2: Populate non-deterministic fields using LLM
	log.Println("\n=== Step 2: Populate Non-Deterministic Fields ===")
	agentResponse, err := workflow.Populate(ctx, onboard, fileContents)
	if err != nil {
		log.Fatalf("❌ Step 2 failed: %v", err)
		os.Exit(1)
	} 

	log.Printf("✅ Non-deterministic values populated and saved to result directory")

	// Step 3: Generate Dalec spec
	log.Println("\n=== Step 3: Generate Dalec Spec ===")
	resolvedTargets, err := workflow.Generate(onboard, fileContents, agentResponse, tag)
	if err != nil {
		log.Fatalf("❌ Step 3 failed: %v", err)
	}

	log.Println("\n━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	log.Printf("✅ Generation Complete at %s", time.Now().Format(time.RFC3339))

	// Step 4: Push to remote repository
	err = workflow.GitPush(onboard.SpecRepository, onboard.SpecImageName, tag)
	if err != nil {
		log.Fatalf("❌ Step 4 failed: %v", err)
	}

	log.Printf("✅ Spec created successfully for repository %s @ %s", onboard.Repository, onboard.Tag)

	// // Step 5: Output a shell variable consist of a list of remote generated spec path separated by comma for CI integration
	// // specs/{repository}/{image}/{image}-{tag}-specfile.yml
	var specPath string
	if onboard.SpecRepository == "" {
		specPath = fmt.Sprintf("specs/%s/%s-%s-specfile.yml", onboard.SpecImageName, onboard.SpecImageName, tag)
	} else {
		specPath = fmt.Sprintf("specs/%s/%s/%s-%s-specfile.yml", onboard.SpecRepository, onboard.SpecImageName, onboard.SpecImageName, tag)
	}
	return specPath, resolvedTargets
}