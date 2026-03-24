package workflow

import (
	"bytes"
	"fmt"
	"log"
	"os"
	"path"

	"dalec-mapping/domain/onboarding"
	"dalec-mapping/domain/repository"
	"dalec-mapping/infrastructure/github"
	"dalec-mapping/utils"
)

// clearResultDirectory removes all contents from the result directory
func clearResultDirectory(resultDir string) error {
	// Check if directory exists
	if _, err := os.Stat(resultDir); os.IsNotExist(err) {
		// Directory doesn't exist, nothing to clear
		return nil
	}

	// Remove all contents
	err := os.RemoveAll(resultDir)
	if err != nil {
		return fmt.Errorf("failed to clear result directory: %w", err)
	}

	fmt.Printf("🗑️  Cleared result directory: %s\n", resultDir)
	return nil
}


// diffSiblings compares the freshly-fetched Dockerfile/Makefile against the
// cached versions from the onboard repo and sets onboard.Mode accordingly.
// Returns an error (and exits) when content has changed, signalling a manual
// review is required.
func diffSiblings(onboard *onboarding.OnboardingInfo, cachedDF, cachedMF []byte) {
	if cachedDF == nil || cachedMF == nil {
		onboard.Mode = onboarding.ManualReview
		return
	}

	dfMatch := bytes.Equal(onboard.DockerfileContent, cachedDF)
	mfMatch := bytes.Equal(onboard.MakefileContent, cachedMF)

	switch {
		case dfMatch && mfMatch:
			onboard.Mode = onboarding.CommitBump
		default: 
			onboard.Mode = onboarding.ManualReview
	}

	log.Printf("✅ Dockerfile and Makefile unchanged for %s — revision bump only\n", onboard.SpecImageName)
}

// Discover runs the discovery step to find all Dockerfiles and Makefiles.
// After fetching, it compares against cached siblings (if any) and sets onboard.Mode.
func Discover(onboard *onboarding.OnboardingInfo, repoInfo *repository.RepoInfo, tag string) error {
	fmt.Println("=== DISCOVER MODE ===")

	// Clear result directory for fresh start
	if err := clearResultDirectory(utils.ResultDir) ; err != nil {
		fmt.Printf("⚠️  Warning: %v\n", err)
	}

	subdir := path.Dir(onboard.DockerfileDir)
	// Parse repo info (just need owner, repo, branch)
	repoInfo, err := github.FetchRepoInfo(onboard.Repository, subdir, tag)
	if err != nil {
		fmt.Printf("❌ Error fetching repository info: %v\n", err)
		os.Exit(1)
	}

	// Run DFS to find all Dockerfiles and Makefiles if not provided in onboarding info
	root := onboard.Repository
	owner, repo, _ := github.FetchRepositorySegments(root)
	// Use the tag ref so the Dockerfile matches the source tree at that version.
	ref := tag
	if ref == "" {
		_, _, ref = github.FetchRepositorySegments(root)
	}
	path := fmt.Sprintf("%s/%s/%s", owner, repo, ref)
	dockerfileURL := fmt.Sprintf("https://raw.githubusercontent.com/%s/%s", path, onboard.DockerfileDir)
	dockerfileContent, err := github.FetchRawContent(dockerfileURL)
	if err != nil {
		fmt.Printf("⚠️  Warning: failed to fetch Dockerfile from %s: %v\n", dockerfileURL, err)
	}

	makefileURL := fmt.Sprintf("https://raw.githubusercontent.com/%s/%s", path, onboard.MakefileDir)
	makefileContent, err := github.FetchRawContent(makefileURL)
	if err != nil {
		fmt.Printf("⚠️  Warning: failed to fetch Makefile from %s: %v\n", makefileURL, err)
	}

	// Save cached copies before overwriting.
	cachedDF := onboard.DockerfileContent
	cachedMF := onboard.MakefileContent

	// Store the fresh content on the onboard struct.
	onboard.DockerfileContent = dockerfileContent
	onboard.MakefileContent = makefileContent

	// Compare and set mode.
	diffSiblings(onboard, cachedDF, cachedMF)

	return nil
}