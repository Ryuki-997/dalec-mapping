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
	"strings"

	"dalec-mapping/domain/onboarding"
	"dalec-mapping/infrastructure/ado"
	"dalec-mapping/infrastructure/github"
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

	utils.SpecRepoFetchCachedBuildFiles(onboard)

	dockerfilePath := ""
	if onboard.DockerfileDir != "" {
		dockerfilePath = resolveFilePath(onboard.DockerfileDir, "Dockerfile")
	}
	makefilePath := ""
	if onboard.MakefileDir != "" {
		makefilePath = resolveFilePath(onboard.MakefileDir, "Makefile")
	}

	// Paths are relative to the repository starting point. When the URL
	// contains a component suffix (e.g. owner/repo/cns), prefix the resolved
	// paths so they are correct from the repo root.
	componentPath := extractComponentPath(repoURL)
	if componentPath != "" {
		if dockerfilePath != "" {
			dockerfilePath = path.Join(componentPath, dockerfilePath)
		}
		if makefilePath != "" {
			makefilePath = path.Join(componentPath, makefilePath)
		}
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

// resolveFilePath resolves a partner-provided path to a full file path.
// The path may be either a directory (e.g. ".", "cns") or an explicit file
// (e.g. "./docker/Dockerfile"). When the path looks like a file (its base
// name matches fileName or contains a dot extension), it is returned directly.
// Otherwise it is treated as a directory and fileName is appended.
func resolveFilePath(dirPath, fileName string) string {
	if dirPath == "" {
		return ""
	}

	baseName := path.Base(dirPath)
	isFile := baseName == fileName || (strings.Contains(baseName, ".") && baseName != "." && baseName != "..")

	if isFile {
		return path.Clean(dirPath)
	}

	dir := dirPath
	if dir == "." {
		dir = ""
	}
	if dir == "" {
		return fileName
	}
	return path.Clean(dir + "/" + fileName)
}

// fetchBuildFiles dispatches to the ADO or GitHub fetcher based on the repo URL.
func fetchBuildFiles(repoURL, dockerfilePath, makefilePath, tag string) ([]byte, []byte, error) {
	if ado.IsADORepo(repoURL) {
		return fetchBuildFilesFromADO(repoURL, dockerfilePath, makefilePath, tag)
	}
	return fetchBuildFilesFromGitHub(repoURL, dockerfilePath, makefilePath, tag)
}

// fetchBuildFilesFromADO fetches Dockerfile and Makefile from an ADO repository.
func fetchBuildFilesFromADO(repoURL, dockerfilePath, makefilePath, tag string) ([]byte, []byte, error) {
	if _, err := ado.FetchADORepoInfo(repoURL); err != nil {
		return nil, nil, fmt.Errorf("failed to fetch repository info: %w", err)
	}

	var dockerfileContent, makefileContent []byte

	if dockerfilePath != "" {
		content, err := ado.FetchADOFileContent(repoURL, dockerfilePath, tag)
		if err != nil {
			log.Printf("⚠️  Warning: failed to fetch Dockerfile: %v\n", err)
		} else {
			dockerfileContent = content
		}
	}

	if makefilePath != "" {
		content, err := ado.FetchADOFileContent(repoURL, makefilePath, tag)
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
	repoInfo, err := github.FetchRepoInfo(repoURL)
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
		content, err := github.FetchRawContent(dockerfileURL)
		if err != nil {
			log.Printf("⚠️  Warning: failed to fetch Dockerfile from %s: %v\n", dockerfileURL, err)
		} else {
			dockerfileContent = content
		}
	}

	if makefilePath != "" {
		makefileURL := fmt.Sprintf("https://raw.githubusercontent.com/%s/%s", rawPath, makefilePath)
		content, err := github.FetchRawContent(makefileURL)
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
		if !bytes.Equal(bytes.TrimRight(onboard.DockerfileContent, "\n"), bytes.TrimRight(cachedDF, "\n")) {
			log.Printf("Dockerfile changed for %s\n", onboard.SpecImageName)
			changed = true
		}
	}
	if cachedMF != nil && onboard.MakefileContent != nil {
		if !bytes.Equal(bytes.TrimRight(onboard.MakefileContent, "\n"), bytes.TrimRight(cachedMF, "\n")) {
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

// extractComponentPath returns the component subdirectory from a repository URL.
// Returns "" when the URL has no component suffix (i.e. the project lives at root).
func extractComponentPath(repoURL string) string {
	if ado.IsADORepo(repoURL) {
		_, componentPath := ado.SplitADOComponent(repoURL)
		return componentPath
	}
	_, componentPath := github.SplitGitHubComponent(repoURL)
	return componentPath
}
