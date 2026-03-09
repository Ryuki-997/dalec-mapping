package workflow

import (
	"fmt"
	"os"
	"path"

	"dalec-mapping/domain/llm"
	"dalec-mapping/domain/onboarding"
	"dalec-mapping/domain/repository"
	"dalec-mapping/infrastructure/github"
	"dalec-mapping/utils"
)

// Discover runs the discovery step to find all Dockerfiles and Makefiles
func Discover(onboard *onboarding.OnboardingInfo, fileContents *llm.InstructionContents, repoInfo *repository.RepoInfo, tag string) error {
	fmt.Println("=== DISCOVER MODE ===")

	// Clear result directory for fresh start
	if err := github.ClearResultDirectory(utils.ResultDir) ; err != nil {
		fmt.Printf("⚠️  Warning: %v\n", err)
	}

	subdir := path.Dir(onboard.Dockerfile)
	// Parse repo info (just need owner, repo, branch)
	repoInfo, err := github.FetchRepoInfo(onboard.Repository, subdir, tag)
	if err != nil {
		fmt.Printf("❌ Error fetching repository info: %v\n", err)
		os.Exit(1)
	}

	// Run DFS to find all Dockerfiles and Makefiles if not provided in onboarding info
	root := onboard.Repository
	owner, repo, branch := github.ExtractRepositorySegments(root)
	path := fmt.Sprintf("%s/%s/%s", owner, repo, branch)
	dockerfileURL := fmt.Sprintf("https://raw.githubusercontent.com/%s/%s", path, onboard.Dockerfile)
	dockerfileContent, err := github.FetchRawContent(dockerfileURL)
	if err != nil {
		fmt.Printf("⚠️  Warning: failed to fetch Dockerfile from %s: %v\n", dockerfileURL, err)
	}

	makefileURL := fmt.Sprintf("https://raw.githubusercontent.com/%s/%s", path, onboard.Makefile)
	makefileContent, err := github.FetchRawContent(makefileURL)
	if err != nil {
		fmt.Printf("⚠️  Warning: failed to fetch Makefile from %s: %v\n", makefileURL, err)
	}

	fileContents.Dockerfile = dockerfileContent
	fileContents.Makefile = makefileContent

	return nil
}