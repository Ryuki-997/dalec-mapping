package main

import (
	"azure-spec-generation/tool"
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/joho/godotenv"
)

type Config struct {
	Repo                  string
	GitHubToken           string
	AzureOpenAIEndpoint   string
	AzureOpenAIKey        string
	AzureOpenAIDeployment string
}

func main() {
	wd, _ := os.Getwd()
	log.Printf("Working directory: %s", wd)
	
	err := godotenv.Load()
	if err != nil {
		log.Printf("⚠️  .env file not found at %s/.env: %v", wd, err)
		os.Exit(1)
	} 

	log.Println("Dalec Spec Generator - Scheduled Job")
	log.Printf("Started at: %s", time.Now().Format(time.RFC3339))
	log.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")

	ctx := context.Background()

	onboard := &tool.OnboardingInfo{}
	log.Printf("Looking for onboard file at: %s", tool.OnboardFilePath)
	err = tool.ParseOnboardFile(tool.OnboardFilePath, onboard)
	if err != nil {
		log.Fatalf("❌ Failed to parse onboard file: %v", err)
	}

	fmt.Printf("Repository mounted: %v\n", onboard.Repository)

	// Step 1: Discover build files
	log.Println("\n=== Step 1: Discover Build Files ===")
	filepaths, err := tool.Discover(ctx, onboard.Repository)
	if err != nil {
		log.Fatalf("❌ Step 1 failed: %v", err)
	}

	log.Printf("✅ Build files discovered: %+v", filepaths)

	// Step 2: Populate non-deterministic fields using LLM
	log.Println("\n=== Step 2: Populate Non-Deterministic Fields ===")
	err = tool.Populate(ctx, onboard.Repository)
	if err != nil {
		log.Fatalf("❌ Step 2 failed: %v", err)
		os.Exit(1)
	} 

	// Step 3: Generate Dalec spec
	log.Println("\n=== Step 3: Generate Dalec Spec ===")
	specPath, err := tool.Generate(ctx, onboard.Repository)
	if err != nil {
		log.Fatalf("❌ Step 3 failed: %v", err)
	}
	log.Printf("✅ Dalec spec generated: %s", specPath)

	log.Println("\n━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	log.Printf("✅ Generation Complete at %s", time.Now().Format(time.RFC3339))
}
