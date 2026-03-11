package main

import (
	"context"
	"dalec-mapping/domain/llm"
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

	log.Println("Input path for onboarding files:", *inputPath)
	
	wd, _ := os.Getwd()
	log.Printf("Working directory: %s", wd)

	err := godotenv.Load()
	if err != nil {
		log.Printf("⚠️  .env file not found at %s/.env: %v", wd, err)
	}
	
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
		for _, tag := range onboard.Tag {
			// shortTag is the plain "vX.Y.Z" used for image naming and spec paths;
			// the full tag (e.g. "azure-ipam/v0.4.0") is passed into the pipeline
			// so git ref lookups work without a second API call.
			shortTag := semver.StripToSemver(tag)
			log.Printf("▶ Running pipeline for %s @ %s\n", onboard.Repository, tag)
			remotePath, resolvedTargets := generateSpec(&onboard, tag)
			shellVar = append(shellVar, remotePath)

			testAndPush(&onboard, remotePath, shortTag, tag, resolvedTargets)
		}
	}

	log.Printf("specPaths=%s\n", strings.Join(shellVar, ","))

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

	log.Printf("✅ Spec created successfully for repository %s @ %s", onboard.Repository, onboard.Tag)

	// // Step 4: Output a shell variable consist of a list of remote generated spec path separated by comma for CI integration
	// // specs/{repository}/{image}/{image}-{tag}-specfile.yml
	shortTag := semver.StripToSemver(tag)
	var specPath string
	if onboard.SpecRepository == "" {
		specPath = fmt.Sprintf("specs/%s/%s-%s-specfile.yml", onboard.SpecImageName, onboard.SpecImageName, shortTag)
	} else {
		specPath = fmt.Sprintf("specs/%s/%s/%s-%s-specfile.yml", onboard.SpecRepository, onboard.SpecImageName, onboard.SpecImageName, shortTag)
	}
	return specPath, resolvedTargets
}

func testAndPush(onboard *onboarding.OnboardingInfo, remotePath, shortTag, tag string, resolvedTargets []string) {
	// Step 5: build, run, and test the image before pushing
	if err := workflow.ImageTest(remotePath, onboard.SpecImageName, shortTag, resolvedTargets); err != nil {
		log.Fatalf("❌ Image test failed for %s @ %s: %v", onboard.SpecImageName, shortTag, err)
	}

	// Step 6: Push to remote repository (only after image test passes)
	if err := workflow.GitPush(onboard.SpecRepository, onboard.SpecImageName, tag); err != nil {
		log.Fatalf("❌ Step 6 failed: %v", err)
	}
}