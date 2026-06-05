// ═══════════════════════════════════════════════════════════════════════════════
// Discover —
//
//   Fetches the source Dockerfile and Makefile from the partner repository for
//   the workitem's current tag and stores them on component.BuildFiles. Template
//   diffing for BUMP-VERSION is now done against the spec repo's BuildFiles
//   snapshots (see semver.FindTemplateVersion + specapi.SpecRepoFetchFile),
//   so the partner repo is only consulted for the in-progress tag.
//
//   Functions are ordered by call sequence:
//     DiscoverBuildFiles()       — fetch new tag's files, store on component
//     FetchBuildFilesAtTag()     — fetch any tag's files, return bytes only
//       → fetchBuildFiles()
//           → fetchBuildFilesFromADO()
//           → fetchBuildFilesFromGitHub()
//       → clearResultDirectory()
// ═══════════════════════════════════════════════════════════════════════════════

package partnerrepo

import (
	"fmt"
	"log"
	"path"
	"strings"

	"dalec-mapping/domain/workplan"
	"dalec-mapping/workflow/infrastructure/ado"
	"dalec-mapping/workflow/infrastructure/github"
)

// DiscoverBuildFiles fetches the Dockerfile and Makefile from the source repo
// at component.Tag.Full and stores them on component.BuildFiles. The owning WorkGroup
// (component.Group) carries the upstream repository URL; the Dockerfile/Makefile
// sub-paths live on the component itself.
func DiscoverBuildFiles(component *workplan.WorkComponent) error {
	dockerfileContent, makefileContent, err := FetchBuildFilesAtTag(component, component.Tag.Full)
	if err != nil {
		return err
	}

	component.BuildFiles.Dockerfile.Source = dockerfileContent
	component.BuildFiles.Makefile.Source = makefileContent

	log.Printf("✅ Discover complete for %s\n", component.Naming.SpecFileName)
	return nil
}

// FetchBuildFilesAtTag fetches the Dockerfile and Makefile from the partner
// repo at the supplied tag without mutating the work component. Used by the
// orchestration layer to fetch the template version's files for comparison.
func FetchBuildFilesAtTag(component *workplan.WorkComponent, tag string) ([]byte, []byte, error) {
	componentNaming := component.Naming
	repoURL := component.Group.Repository

	log.Println()
	log.Printf("── Fetch build files: %s @ %s ──\n", componentNaming.SpecImageName, tag)
	log.Printf("Repository: %s\n", repoURL)

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

	return fetchBuildFiles(repoURL, dockerfilePath, makefilePath, tag)
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
	if _, _, err := ado.FetchADORepoInfo(repoURL); err != nil {
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
	repoInfo, _, err := github.FetchRepoInfo(repoURL)
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

// diffSiblings was removed: comparison now lives in the orchestration layer
// using SpecRepoFetchBuildFilesForVersion to fetch the per-version snapshot.

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
