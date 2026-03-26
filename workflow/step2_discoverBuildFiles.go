// ═══════════════════════════════════════════════════════════════════════════════
// Step 2 — Discover
//
//   Fetches the source Dockerfile and Makefile for the given tag, compares them
//   against cached siblings from a previous onboard, and sets onboard.Mode to
//   either CommitBump (unchanged), ContentChanged, or FirstOnboard.
//
//   Chunk 1 · MAIN    DiscoverBuildFiles()
//   Chunk 2 · HELPERS diffSiblings(), clearResultDirectory()
// ═══════════════════════════════════════════════════════════════════════════════

package workflow

import (
	"bytes"
	"fmt"
	"log"
	"os"
	"path"

	"dalec-mapping/domain/onboarding"
	repo "dalec-mapping/domain/repository"
	"dalec-mapping/infrastructure/repository"
	"dalec-mapping/utils"
)

// ─── Chunk 1 · MAIN ─────────────────────────────────────────────────────────

// DiscoverBuildFiles fetches the Dockerfile and Makefile from the source repo at the
// given tag, diffs them against cached siblings, and sets onboard.Mode.
func DiscoverBuildFiles(onboard *onboarding.OnboardingInfo, repoInfo *repo.RepoInfo, tag string) error {
	// Clear the result directory for a fresh start
	if err := clearResultDirectory(utils.ResultDir); err != nil {
		fmt.Printf("⚠️  Warning: %v\n", err)
	}

	// Fetch repo metadata (populates repoInfo)
	subdir := path.Dir(onboard.DockerfileDir)
	repoInfo, err := repository.FetchRepoInfo(onboard.Repository, subdir, tag)
	if err != nil {
		fmt.Printf("❌ Error fetching repository info: %v\n", err)
		os.Exit(1)
	}

	// Build the raw content path: owner/repo/ref
	root := onboard.Repository
	owner, repoName, _ := repository.FetchRepositorySegments(root)
	ref := tag
	if ref == "" {
		_, _, ref = repository.FetchRepositorySegments(root)
	}
	rawPath := fmt.Sprintf("%s/%s/%s", owner, repoName, ref)

	// Fetch fresh Dockerfile and Makefile from the source repo
	dockerfileURL := fmt.Sprintf("https://raw.githubusercontent.com/%s/%s", rawPath, onboard.DockerfileDir)
	dockerfileContent, err := repository.FetchRawContent(dockerfileURL)
	if err != nil {
		fmt.Printf("⚠️  Warning: failed to fetch Dockerfile from %s: %v\n", dockerfileURL, err)
	}

	makefileURL := fmt.Sprintf("https://raw.githubusercontent.com/%s/%s", rawPath, onboard.MakefileDir)
	makefileContent, err := repository.FetchRawContent(makefileURL)
	if err != nil {
		fmt.Printf("⚠️  Warning: failed to fetch Makefile from %s: %v\n", makefileURL, err)
	}

	// Save cached copies before overwriting, then store fresh content
	cachedDF := onboard.DockerfileContent
	cachedMF := onboard.MakefileContent
	onboard.DockerfileContent = dockerfileContent
	onboard.MakefileContent = makefileContent

	// Compare fresh vs cached and set onboard.Mode
	diffSiblings(onboard, cachedDF, cachedMF)

	return nil
}

// ─── Chunk 2 · HELPERS ──────────────────────────────────────────────────────

// diffSiblings compares fresh Dockerfile/Makefile against cached versions and
// sets onboard.Mode to CommitBump (unchanged), ContentChanged, or FirstOnboard.
func diffSiblings(onboard *onboarding.OnboardingInfo, cachedDF, cachedMF []byte) {
	if cachedDF == nil || cachedMF == nil {
		onboard.Mode = onboarding.FirstOnboard
		return
	}

	dfMatch := bytes.Equal(onboard.DockerfileContent, cachedDF)
	mfMatch := bytes.Equal(onboard.MakefileContent, cachedMF)

	switch {
	case dfMatch && mfMatch:
		onboard.Mode = onboarding.CommitBump
	default:
		onboard.Mode = onboarding.ContentChanged
	}

	log.Printf("✅ Dockerfile and Makefile unchanged for %s — commit bump only\n", onboard.SpecImageName)
}

// clearResultDirectory removes all contents from the result directory.
func clearResultDirectory(resultDir string) error {
	if _, err := os.Stat(resultDir); os.IsNotExist(err) {
		return nil
	}
	if err := os.RemoveAll(resultDir); err != nil {
		return fmt.Errorf("failed to clear result directory: %w", err)
	}
	fmt.Printf("🗑️  Cleared result directory: %s\n", resultDir)
	return nil
}