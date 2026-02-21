package workflow

import (
	"fmt"
	"os"

	"dalec-mapping/domain/llm"
	"dalec-mapping/domain/onboarding"
	"dalec-mapping/domain/repository"
	"dalec-mapping/infrastructure/github"
	"dalec-mapping/utils"
)

// Discover runs the discovery step to find all Dockerfiles and Makefiles
func Discover(onboard *onboarding.OnboardingInfo, fileContents *llm.InstructionContents, repoInfo *repository.RepoInfo) error {
	fmt.Println("=== DISCOVER MODE ===")

	// Clear result directory for fresh start
	if err := github.ClearResultDirectory(utils.ResultDir) ; err != nil {
		fmt.Printf("⚠️  Warning: %v\n", err)
	}

	// Parse repo info (just need owner, repo, branch)
	tag := ""
	if len(onboard.Tag) > 0 {
		tag = onboard.Tag[0]
	}
	repoInfo, err := github.FetchRepoInfo(onboard.Repository, tag)
	if err != nil {
		fmt.Printf("❌ Error fetching repository info: %v\n", err)
		os.Exit(1)
	}

	// Run DFS to find all Dockerfiles and Makefiles if not provided in onboarding info
	root := onboard.Repository

	err = github.FindFiles(root, fileContents)
	if err != nil {
		fmt.Printf("❌ Error discovering build files: %v\n", err)
		os.Exit(1)
	}

	return nil
}