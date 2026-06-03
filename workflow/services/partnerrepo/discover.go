// ═══════════════════════════════════════════════════════════════════════════════
// Discover —
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

package partnerrepo

import (
	"bytes"
	"fmt"
	"log"
	"os"
	"path"
	"strings"

	"dalec-mapping/config"
	"dalec-mapping/domain/naming"
	"dalec-mapping/domain/workplan"
	"dalec-mapping/workflow/infrastructure/ado"
	"dalec-mapping/workflow/infrastructure/github"
	"dalec-mapping/workflow/infrastructure/specapi"
)

// DiscoverBuildFiles fetches the Dockerfile and Makefile from the source repo at the
// given tag, diffs them against cached siblings, and returns whether content changed.
// Populates item.BuildFiles.Dockerfile.Source and item.BuildFiles.Makefile.Source.
func DiscoverBuildFiles(item *workplan.WorkItem) (bool, error) {
	component := item.Naming
	tag := item.Tag.Full
	repoURL := component.Repository

	log.Println()
	log.Printf("── Discover: %s @ %s ──\n", component.SpecImageName, tag)
	log.Printf("Repository: %s\n", repoURL)
	log.Println()

	if err := clearResultDirectory(config.ResultDir); err != nil {
		log.Printf("⚠️  Warning: %v\n", err)
	}

	cachedDF, cachedMF := specapi.SpecRepoFetchCachedBuildFiles(component)

	dockerfilePath := ""
	if component.DockerfileDir != "" {
		dockerfilePath = resolveFilePath(component.DockerfileDir, "Dockerfile")
	}
	makefilePath := ""
	if component.MakefileDir != "" {
		makefilePath = resolveFilePath(component.MakefileDir, "Makefile")
	}

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

	dockerfileContent, makefileContent, err := fetchBuildFiles(repoURL, dockerfilePath, makefilePath, tag, component.License)
	if err != nil {
		return false, err
	}

	item.BuildFiles.Dockerfile.Source = dockerfileContent
	item.BuildFiles.Makefile.Source = makefileContent

	contentChanged := diffSiblings(item, component, cachedDF, cachedMF)

	log.Printf("Output: contentChanged=%v\n", contentChanged)
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
func fetchBuildFiles(repoURL, dockerfilePath, makefilePath, tag, configuredLicense string) ([]byte, []byte, error) {
	if ado.IsADORepo(repoURL) {
		return fetchBuildFilesFromADO(repoURL, dockerfilePath, makefilePath, tag, configuredLicense)
	}
	return fetchBuildFilesFromGitHub(repoURL, dockerfilePath, makefilePath, tag, configuredLicense)
}

// fetchBuildFilesFromADO fetches Dockerfile and Makefile from an ADO repository.
func fetchBuildFilesFromADO(repoURL, dockerfilePath, makefilePath, tag, configuredLicense string) ([]byte, []byte, error) {
	if _, err := ado.FetchADORepoInfo(repoURL, configuredLicense); err != nil {
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
func fetchBuildFilesFromGitHub(repoURL, dockerfilePath, makefilePath, tag, configuredLicense string) ([]byte, []byte, error) {
	repoInfo, err := github.FetchRepoInfo(repoURL, configuredLicense)
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

// diffSiblings compares the freshly-fetched Dockerfile/Makefile (on
// item.BuildFiles.Dockerfile.Source / item.BuildFiles.Makefile.Source) against
// cached versions and returns true if content has changed. Only compares files
// that have both a fresh and cached version (skips files where either side is
// nil/empty).
func diffSiblings(item *workplan.WorkItem, component naming.Naming, cachedDF, cachedMF []byte) bool {
	if cachedDF == nil && cachedMF == nil {
		return false
	}

	freshDF := item.BuildFiles.Dockerfile.Source
	freshMF := item.BuildFiles.Makefile.Source
	changed := false
	if cachedDF != nil && freshDF != nil {
		if !bytes.Equal(bytes.TrimRight(freshDF, "\n"), bytes.TrimRight(cachedDF, "\n")) {
			log.Printf("Dockerfile changed for %s\n", component.SpecImageName)
			changed = true
		}
	}
	if cachedMF != nil && freshMF != nil {
		if !bytes.Equal(bytes.TrimRight(freshMF, "\n"), bytes.TrimRight(cachedMF, "\n")) {
			log.Printf("Makefile changed for %s\n", component.SpecImageName)
			changed = true
		}
	}

	if !changed {
		log.Printf("✅ Build files unchanged for %s\n", component.SpecImageName)
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
