package tool

import (
	"fmt"
	"os"

	"dalec-mapping/github"
	"dalec-mapping/global"
)

// Discover runs the discovery step to find all Dockerfiles and Makefiles
func Discover(onboard *global.OnboardingInfo, fileContents *global.InstructionContents) error {
	fmt.Println("=== DISCOVER MODE ===")

	// Clear result directory for fresh start
	if err := github.ClearResultDirectory(global.ResultDir) ; err != nil {
		fmt.Printf("⚠️  Warning: %v\n", err)
	}

	// // Parse repo info (just need owner, repo, branch)
	// repoInfo, err := fetchGitHubRepoInfo(onboard.Repository, onboard.Tag)
	// if err != nil {
	// 	fmt.Printf("❌ Error fetching repository info: %v\n", err)
	// 	os.Exit(1)
	// }

	// Run DFS to find all Dockerfiles and Makefiles if not provided in onboarding info
	root := onboard.Repository
	

	err := github.FindFiles(root, fileContents)
	if err != nil {
		fmt.Printf("❌ Error discovering build files: %v\n", err)
		os.Exit(1)
	}

	return nil
}

func fetchGitHubRepoInfo(repoPath, tag string) (*global.RepoInfo, error) {
	// Fetch GitHub repository information
	fmt.Println("=== FETCHING GITHUB METADATA ===")
	repoInfo, err := github.FetchRepoInfo(repoPath)
	if err != nil {
		fmt.Printf("❌ Error fetching repository info: %v\n", err)
		return nil, err
	}

	// If tag is specified, fetch that tag's commit SHA
	if tag != "" {
		fmt.Printf("Fetching tag: %s\n", tag)
		err = github.FetchTagInfo(repoInfo, tag)
		if err != nil {
			fmt.Printf("❌ Error fetching tag info: %v\n", err)
			return nil, err
		}
	}

	github.PrintRepoInfo(repoInfo)

	return repoInfo, nil
}