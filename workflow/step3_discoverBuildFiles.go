// ═══════════════════════════════════════════════════════════════════════════════
// Step 3 — Discover
//
//   Fetches the source Dockerfile and Makefile for the given tag, compares them
//   against cached siblings from a previous onboard, and returns whether
//   content has changed.
//
//   Functions are ordered by call sequence:
//     DiscoverBuildFiles()
//       → loadCachedBuildFiles()
//       → fetchBuildFiles()
//           → fetchBuildFilesFromADO()
//           → fetchBuildFilesFromGitHub()
//       → diffSiblings()
//       → clearResultDirectory()
// ═══════════════════════════════════════════════════════════════════════════════

package workflow

import (
	"bytes"
	"fmt"
	"log"
	"os"
	"path"

	"dalec-mapping/domain/onboarding"
	"dalec-mapping/infrastructure/repository"
	"dalec-mapping/pipeline"
	"dalec-mapping/utils"
)

// DiscoverBuildFiles fetches the Dockerfile and Makefile from the source repo at the
// given tag, diffs them against cached siblings, and returns whether content changed.
// DockerfileDir and MakefileDir are treated as directory paths (e.g. "." or "cns").
// The actual filenames (Dockerfile, Makefile) are discovered by listing the
// directory one level deep.
func DiscoverBuildFiles() (bool, error) {
	onboard := pipeline.Current.Onboard
	tag := pipeline.Current.Tag.Full
	repoURL := onboard.Repository

	log.Println()
	log.Printf("── Discover: %s @ %s ──\n", onboard.SpecImageName, tag)
	log.Printf("Repository: %s\n", repoURL)
	log.Println()

	if err := clearResultDirectory(utils.ResultDir); err != nil {
		log.Printf("⚠️  Warning: %v\n", err)
	}

	loadCachedBuildFiles(onboard)

	var componentPath string
	if repository.IsADORepo(repoURL) {
		_, componentPath = repository.SplitADOComponent(repoURL)
	} else {
		_, componentPath = repository.SplitGitHubComponent(repoURL)
	}

	dockerfilePath := ""
	if onboard.DockerfileDir != "" {
		dockerfilePath = resolveFilePath(componentPath, onboard.DockerfileDir, "Dockerfile")
	}
	makefilePath := ""
	if onboard.MakefileDir != "" {
		makefilePath = resolveFilePath(componentPath, onboard.MakefileDir, "Makefile")
	}

	log.Printf("  Dockerfile path: %s\n", dockerfilePath)
	log.Printf("  Makefile path:   %s\n", makefilePath)
	log.Println()

	dockerfileContent, makefileContent, err := fetchBuildFiles(repoURL, dockerfilePath, makefilePath, tag)
	if err != nil {
		return false, err
	}

	cachedDF := onboard.DockerfileContent
	cachedMF := onboard.MakefileContent
	onboard.DockerfileContent = dockerfileContent
	onboard.MakefileContent = makefileContent

	contentChanged := diffSiblings(onboard, cachedDF, cachedMF)

	log.Printf("Step 3 output: contentChanged=%v\n", contentChanged)
	return contentChanged, nil
}

// resolveFilePath resolves a directory path (from onboard.yml) relative to a
// component directory and appends the given filename (e.g. "Dockerfile", "Makefile").
// Returns the full file path ready for fetching from the source repository.
func resolveFilePath(componentPath, dirPath, fileName string) string {
	if dirPath == "" {
		return ""
	}
	dir := dirPath
	if dir == "." {
		dir = ""
	}
	if componentPath != "" && dir != "" {
		dir = componentPath + "/" + dir
	} else if componentPath != "" {
		dir = componentPath
	}
	if dir == "" {
		return fileName
	}
	return path.Clean(dir + "/" + fileName)
}

// loadCachedBuildFiles fetches the previously-committed Dockerfile/Makefile
// from the spec repo's onboard directory. These cached files are used to
// detect content changes during the diff step.
func loadCachedBuildFiles(component *onboarding.ComponentConfig) {
	rawBaseURL := fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/%s",
		utils.OnboardOwner, utils.OnboardRepo, utils.OnboardBranch)

	dockerfilePath := component.OnboardDir + "/Dockerfile"
	dockerfileContent, err := repository.FetchRawContent(rawBaseURL + "/" + dockerfilePath)
	if err == nil {
		component.DockerfileContent = dockerfileContent
	}

	makefilePath := component.OnboardDir + "/Makefile"
	makefileContent, err := repository.FetchRawContent(rawBaseURL + "/" + makefilePath)
	if err == nil {
		component.MakefileContent = makefileContent
	}

	hasDockerfile := component.DockerfileContent != nil
	hasMakefile := component.MakefileContent != nil
	if !hasDockerfile && !hasMakefile {
		log.Printf("No sibling Dockerfile/Makefile found for %s — treating as first-time onboard\n", component.SpecImageName)
		return
	}
	log.Printf("Found existing siblings for %s (Dockerfile=%v, Makefile=%v) — will diff\n", component.SpecImageName, hasDockerfile, hasMakefile)
}

// fetchBuildFiles dispatches to the ADO or GitHub fetcher based on the repo URL.
func fetchBuildFiles(repoURL, dockerfilePath, makefilePath, tag string) ([]byte, []byte, error) {
	if repository.IsADORepo(repoURL) {
		return fetchBuildFilesFromADO(repoURL, dockerfilePath, makefilePath, tag)
	}
	return fetchBuildFilesFromGitHub(repoURL, dockerfilePath, makefilePath, tag)
}

// fetchBuildFilesFromADO fetches Dockerfile and Makefile from an ADO repository.
func fetchBuildFilesFromADO(repoURL, dockerfilePath, makefilePath, tag string) ([]byte, []byte, error) {
	if _, err := repository.FetchADORepoInfo(repoURL); err != nil {
		return nil, nil, fmt.Errorf("failed to fetch repository info: %w", err)
	}

	var dockerfileContent, makefileContent []byte

	if dockerfilePath != "" {
		content, err := repository.FetchADOFileContent(repoURL, dockerfilePath, tag)
		if err != nil {
			log.Printf("⚠️  Warning: failed to fetch Dockerfile: %v\n", err)
		} else {
			dockerfileContent = content
		}
	}

	if makefilePath != "" {
		content, err := repository.FetchADOFileContent(repoURL, makefilePath, tag)
		if err != nil {
			log.Printf("⚠️  Warning: failed to fetch Makefile: %v\n", err)
		} else {
			makefileContent = content
		}
	}

	return dockerfileContent, makefileContent, nil
}

// fetchBuildFilesFromGitHub fetches Dockerfile and Makefile from a GitHub repository.
func fetchBuildFilesFromGitHub(repoURL, dockerfilePath, makefilePath, tag string) ([]byte, []byte, error) {
	repoInfo, err := repository.FetchRepoInfo(repoURL)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to fetch repository info: %w", err)
	}

	ref := tag
	if ref == "" {
		ref = repoInfo.Branch
	}
	rawPath := fmt.Sprintf("%s/%s/%s", repoInfo.Owner, repoInfo.Repo, ref)

	var dockerfileContent, makefileContent []byte

	if dockerfilePath != "" {
		dockerfileURL := fmt.Sprintf("https://raw.githubusercontent.com/%s/%s", rawPath, dockerfilePath)
		content, err := repository.FetchRawContent(dockerfileURL)
		if err != nil {
			log.Printf("⚠️  Warning: failed to fetch Dockerfile from %s: %v\n", dockerfileURL, err)
		} else {
			dockerfileContent = content
		}
	}

	if makefilePath != "" {
		makefileURL := fmt.Sprintf("https://raw.githubusercontent.com/%s/%s", rawPath, makefilePath)
		content, err := repository.FetchRawContent(makefileURL)
		if err != nil {
			log.Printf("⚠️  Warning: failed to fetch Makefile from %s: %v\n", makefileURL, err)
		} else {
			makefileContent = content
		}
	}

	return dockerfileContent, makefileContent, nil
}

// diffSiblings compares fresh Dockerfile/Makefile against cached versions and
// returns true if content has changed. Only compares files that have both a
// fresh and cached version (skips files where either side is nil/empty).
func diffSiblings(onboard *onboarding.ComponentConfig, cachedDF, cachedMF []byte) bool {
	if cachedDF == nil && cachedMF == nil {
		return false
	}

	changed := false
	if cachedDF != nil && onboard.DockerfileContent != nil {
		if !bytes.Equal(onboard.DockerfileContent, cachedDF) {
			log.Printf("Dockerfile changed for %s\n", onboard.SpecImageName)
			changed = true
		}
	}
	if cachedMF != nil && onboard.MakefileContent != nil {
		if !bytes.Equal(onboard.MakefileContent, cachedMF) {
			log.Printf("Makefile changed for %s\n", onboard.SpecImageName)
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
	log.Printf("Cleared result directory: %s\n", resultDir)
	return nil
}
