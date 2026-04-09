// ═══════════════════════════════════════════════════════════════════════════════
// Step 2 — Discover
//
//   Fetches the source Dockerfile and Makefile for the given tag, compares them
//   against cached siblings from a previous onboard, and sets
//   onboard.ContentChanged accordingly.
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
// given tag, diffs them against cached siblings, and returns whether content changed.
func DiscoverBuildFiles(onboard *onboarding.OnboardingInfo, repoInfo *repo.RepoInfo, tag string) (bool, error) {
	// Clear the result directory for a fresh start
	if err := clearResultDirectory(utils.ResultDir); err != nil {
		fmt.Printf("⚠️  Warning: %v\n", err)
	}

	// Fetch repo metadata (populates repoInfo)
	subdir := "."
	if onboard.DockerfileDir != "" {
		subdir = path.Dir(onboard.DockerfileDir)
	} else if onboard.MakefileDir != "" {
		subdir = path.Dir(onboard.MakefileDir)
	}

	var (
		dockerfileContent []byte
		makefileContent   []byte
		err               error
	)

	if repository.IsADORepo(onboard.Repository) {
		if _, err = repository.FetchADORepoInfo(onboard.Repository, subdir, tag); err != nil {
			return false, fmt.Errorf("failed to fetch repository info: %w", err)
		}
		if onboard.DockerfileDir != "" {
			if dockerfileContent, err = repository.FetchADOFileContent(onboard.Repository, onboard.DockerfileDir, tag); err != nil {
				fmt.Printf("⚠️  Warning: failed to fetch Dockerfile: %v\n", err)
			}
		}
		if onboard.MakefileDir != "" {
			if makefileContent, err = repository.FetchADOFileContent(onboard.Repository, onboard.MakefileDir, tag); err != nil {
				fmt.Printf("⚠️  Warning: failed to fetch Makefile: %v\n", err)
			}
		}
	} else {
		if _, err = repository.FetchRepoInfo(onboard.Repository, subdir, tag); err != nil {
			return false, fmt.Errorf("failed to fetch repository info: %w", err)
		}
		owner, repoName, _ := repository.FetchRepositorySegments(onboard.Repository)
		ref := tag
		if ref == "" {
			_, _, ref = repository.FetchRepositorySegments(onboard.Repository)
		}
		rawPath := fmt.Sprintf("%s/%s/%s", owner, repoName, ref)
		if onboard.DockerfileDir != "" {
			dockerfileURL := fmt.Sprintf("https://raw.githubusercontent.com/%s/%s", rawPath, onboard.DockerfileDir)
			if dockerfileContent, err = repository.FetchRawContent(dockerfileURL); err != nil {
				fmt.Printf("⚠️  Warning: failed to fetch Dockerfile from %s: %v\n", dockerfileURL, err)
			}
		}
		if onboard.MakefileDir != "" {
			makefileURL := fmt.Sprintf("https://raw.githubusercontent.com/%s/%s", rawPath, onboard.MakefileDir)
			if makefileContent, err = repository.FetchRawContent(makefileURL); err != nil {
				fmt.Printf("⚠️  Warning: failed to fetch Makefile from %s: %v\n", makefileURL, err)
			}
		}
	}

	// Save cached copies before overwriting, then store fresh content
	cachedDF := onboard.DockerfileContent
	cachedMF := onboard.MakefileContent
	onboard.DockerfileContent = dockerfileContent
	onboard.MakefileContent = makefileContent

	// Compare fresh vs cached
	contentChanged := diffSiblings(onboard, cachedDF, cachedMF)

	return contentChanged, nil
}

// ─── Chunk 2 · HELPERS ──────────────────────────────────────────────────────

// diffSiblings compares fresh Dockerfile/Makefile against cached versions and
// returns true if content has changed. Only compares files that have both a
// fresh and cached version (skips files where either side is nil/empty).
func diffSiblings(onboard *onboarding.OnboardingInfo, cachedDF, cachedMF []byte) bool {
	if cachedDF == nil && cachedMF == nil {
		return false
	}

	changed := false
	if cachedDF != nil && onboard.DockerfileContent != nil {
		if !bytes.Equal(onboard.DockerfileContent, cachedDF) {
			log.Printf("🔄 Dockerfile changed for %s\n", onboard.SpecImageName)
			changed = true
		}
	}
	if cachedMF != nil && onboard.MakefileContent != nil {
		if !bytes.Equal(onboard.MakefileContent, cachedMF) {
			log.Printf("🔄 Makefile changed for %s\n", onboard.SpecImageName)
			changed = true
		}
	}

	if !changed {
		log.Printf("✅ Build files unchanged for %s\n", onboard.SpecImageName)
	}
	return changed
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