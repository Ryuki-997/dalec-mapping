package main

import (
	"context"
	"log"
	"os"
	"time"

	"github.com/joho/godotenv"

	"dalec-mapping/global"
	"dalec-mapping/tool"
)

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

	onboard := &global.OnboardingInfo{}
	err = global.LoadFile(onboard, global.OnboardFilePath)
	if err != nil {
		log.Fatalf("❌ Failed to parse onboard file: %v", err)
	}

	// Step 1: Discover build files
	fileContents := &global.InstructionContents{
		Dockerfiles: []string{},
		Makefiles:   []string{},
	}
	repositoryInfo := &global.RepoInfo{}
	log.Println("\n=== Step 1: Discover Build Files ===")
	err = tool.Discover(onboard, fileContents, repositoryInfo)
	if err != nil {
		log.Fatalf("❌ Step 1 failed: %v", err)
	}

	log.Printf("✅ Build files discovered: Dockerfiles=%d, Makefiles=%d", len(fileContents.Dockerfiles), len(fileContents.Makefiles))

	ctx := context.Background()

	// Step 2: Populate non-deterministic fields using LLM
	log.Println("\n=== Step 2: Populate Non-Deterministic Fields ===")
	agentResponse, err := tool.Populate(ctx, onboard, fileContents)
	if err != nil {
		log.Fatalf("❌ Step 2 failed: %v", err)
		os.Exit(1)
	} 

	log.Printf("✅ Non-deterministic values populated and saved to result directory")

	// Step 3: Generate Dalec spec
	log.Println("\n=== Step 3: Generate Dalec Spec ===")
	err = tool.Generate(onboard, fileContents, agentResponse)
	if err != nil {
		log.Fatalf("❌ Step 3 failed: %v", err)
	}

	log.Println("\n━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	log.Printf("✅ Generation Complete at %s", time.Now().Format(time.RFC3339))
}
