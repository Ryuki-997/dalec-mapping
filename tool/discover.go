package tool

import (
	"fmt"
	"os"

	"dalec-mapping/github"
	"dalec-mapping/global"
)

// Discover runs the discovery step to find all Dockerfiles and Makefiles
func Discover(onboard *global.OnboardingInfo, fileContents *global.InstructionContents, repoInfo *global.RepoInfo) error {
	fmt.Println("=== DISCOVER MODE ===")

	// Clear result directory for fresh start
	if err := github.ClearResultDirectory(global.ResultDir) ; err != nil {
		fmt.Printf("⚠️  Warning: %v\n", err)
	}

	// Parse repo info (just need owner, repo, branch)
	repoInfo, err := github.FetchRepoInfo(onboard.Repository, onboard.Tag)
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