package workflow

import (
	"fmt"
	"log"
	"strings"

	"dalec-mapping/domain/onboarding"
	"dalec-mapping/infrastructure/ado"
	"dalec-mapping/infrastructure/github"
	"dalec-mapping/infrastructure/semver"
	"dalec-mapping/utils"

	"gopkg.in/yaml.v3"
)

func FetchOnboardFiles(onboardImages *[]onboarding.OnboardingInfo, inputPath string) error {
	// If no input path is provided, search the entire repository
	if inputPath == "" {
		inputPath = "specs" // default to specs directory
	} else {
		inputPath = "specs/" + inputPath
	}

	data, err := github.FetchJSON(fmt.Sprintf("repos/%s/%s/git/trees/%s?recursive=1", utils.OnboardOwner, utils.OnboardRepo, utils.OnboardBranch))
	if err != nil {
		return fmt.Errorf("failed to fetch onboard data: %w", err)
	}

	onboardItems, ok := data["tree"].([]interface{})
	if !ok {
		return fmt.Errorf("unexpected response format: 'tree' field is missing or not an array")
	}

	// Build a set of all file paths in the repo tree for spec existence checks
	treePaths := getExistingFilePaths(onboardItems)

	for _, item := range onboardItems {
		itemMap, ok := item.(map[string]interface{})
		if !ok {
			return fmt.Errorf("unexpected item format: expected a map")
		}

		path, ok := itemMap["path"].(string)
		if !ok {
			return fmt.Errorf("unexpected item format: 'path' field is missing or not a string")
		}
		// Filter paths to only include those within the specified input path
		if !strings.HasPrefix(path, inputPath) {
			continue
		}
		log.Printf("Found file in repo: %s\n", path)

		specRepository, specImageName, err := getOnboardFilepath(path)
		if err != nil {
			continue
		}

		rawURL := fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/%s/%s", utils.OnboardOwner, utils.OnboardRepo, utils.OnboardBranch, path)
		content, err := github.FetchRawContent(rawURL)
		if err != nil {
			return fmt.Errorf("failed to fetch onboard file %s: %w", path, err)
		}

		if len(content) == 0 {
			log.Printf("⚠️  Skipping empty onboard file: %s\n", path)
			continue
		}

		onboard := onboarding.OnboardingInfo{
			Repository: "",
			Tag:        []string{},
			DockerfileDir: "",
			MakefileDir:   "",
			SpecImageName: specImageName,
			SpecRepository: specRepository,
			OnboardDir: path[:strings.LastIndex(path, "/")],
		}

		if err := yaml.Unmarshal(content, &onboard); err != nil {
			return fmt.Errorf("failed to unmarshal onboard data: %w", err)
		}

		// Check for sibling Dockerfile and Makefile in the onboard repo folder.
		// Their presence indicates this image has been onboarded before.
		siblingDockerfile := onboard.OnboardDir + "/Dockerfile"
		siblingMakefile := onboard.OnboardDir + "/Makefile"
		hasDockerfile := treePaths[siblingDockerfile]
		hasMakefile := treePaths[siblingMakefile]

		if !hasDockerfile || !hasMakefile {
			log.Printf("No sibling Dockerfile/Makefile found for %s — treating as first-time onboard\n", specImageName)
		} else {
			// Existing onboard — fetch cached siblings for diff comparison in Discover.
			rawBase := fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/%s", utils.OnboardOwner, utils.OnboardRepo, utils.OnboardBranch)
			cachedDF, err := github.FetchRawContent(rawBase + "/" + siblingDockerfile)
			if err != nil {
				return fmt.Errorf("failed to fetch cached Dockerfile %s: %w", siblingDockerfile, err)
			}
			cachedMF, err := github.FetchRawContent(rawBase + "/" + siblingMakefile)
			if err != nil {
				return fmt.Errorf("failed to fetch cached Makefile %s: %w", siblingMakefile, err)
			}
			onboard.DockerfileContent = cachedDF
			onboard.MakefileContent = cachedMF
			log.Printf("📂 Found existing Dockerfile and Makefile siblings for %s — will diff in Discover\n", specImageName)
		}
		
		if onboard.Tag == nil {
			onboard.Tag = append(onboard.Tag, "latest")
		}

		resolvedTags, err := resolveTagsForRepo(onboard.Repository, onboard.Tag)
		if err != nil {
			log.Printf("⚠️  Failed to resolve tags for %s: %v\n", onboard.Repository, err)
			continue
		}
		log.Printf("✅ Resolved tags for %s: %v (from patterns: %v)\n", onboard.Repository, resolvedTags, onboard.Tag)

		// Filter out resolved tags whose spec files already exist in the remote repo
		filteredTags := getFilteredTags(resolvedTags, onboard.SpecRepository, onboard.SpecImageName, treePaths)

		if hasDockerfile && hasMakefile {
			// For re-onboard (revision bump), we want tags that ALREADY have specs.
			existingTags := getExistingTags(resolvedTags, onboard.SpecRepository, onboard.SpecImageName, treePaths)
			if len(existingTags) > 0 {
				onboard.Tag = existingTags
				*onboardImages = append(*onboardImages, onboard)
			}
		} else if len(filteredTags) > 0 {
			onboard.Tag = filteredTags
			*onboardImages = append(*onboardImages, onboard)
		}

		log.Printf("Onboard Data: %v\n", onboard)
	}
	return nil
}

func getOnboardFilepath(path string) (specRepository string, specImageName string, err error) {
	parts := strings.Split(path, "/")
	n := len(parts)
	if parts[n-1] != "onboard.yml" {
		return "", "", fmt.Errorf("not an onboard file: %s", path)
	}

	switch n {
	case 4: 
		specRepository, specImageName = parts[1], parts[2]
	case 3: 
		specRepository, specImageName = "", parts[1]
	default:
		return "", "", fmt.Errorf("unexpected file path format: %s", path)
	}

	return specRepository, specImageName, nil
}

func getExistingFilePaths(items []interface{}) map[string]bool {
	treePaths := make(map[string]bool)
	for _, item := range items {
		if m, ok := item.(map[string]interface{}); ok {
			if p, ok := m["path"].(string); ok {
				treePaths[p] = true
			}
		}
	}
	return treePaths
}

func getFilteredTags(resolvedTags []string, specRepo, specImage string, existingPaths map[string]bool) []string {
	var filteredTags []string
	for _, tag := range resolvedTags {
		shortTag := semver.StripToSemver(tag)
		specPath := specFilePath(specRepo, specImage, shortTag)
		if existingPaths[specPath] {
			log.Printf("⏭  Skipping %s @ %s: spec file already exists at %s\n", specImage, tag, specPath)
			continue
		}
		filteredTags = append(filteredTags, tag)
	}
	return filteredTags
}

// getExistingTags is the inverse of getFilteredTags — returns tags that already have specs.
func getExistingTags(resolvedTags []string, specRepo, specImage string, existingPaths map[string]bool) []string {
	var existing []string
	for _, tag := range resolvedTags {
		shortTag := semver.StripToSemver(tag)
		specPath := specFilePath(specRepo, specImage, shortTag)
		if existingPaths[specPath] {
			existing = append(existing, tag)
		}
	}
	return existing
}

// specFilePath returns the remote path for a spec file.
func specFilePath(specRepo, specImage, shortTag string) string {
	if specRepo != "" {
		return fmt.Sprintf("specs/%s/%s/%s-%s-specfile.yml", specRepo, specImage, specImage, shortTag)
	}
	return fmt.Sprintf("specs/%s/%s-%s-specfile.yml", specImage, specImage, shortTag)
}

// isADORepo returns true when the repository URL points to an Azure DevOps repo
// (e.g. https://dev.azure.com/... or dev.azure.com/...).
func isADORepo(repoURL string) bool {
	normalized := strings.TrimPrefix(strings.TrimPrefix(repoURL, "https://"), "http://")
	return strings.HasPrefix(normalized, "dev.azure.com/") ||
		strings.HasPrefix(normalized, "ssh.dev.azure.com/") ||
		strings.Contains(normalized, ".visualstudio.com/")
}

// resolveTagsForRepo fetches tags from the appropriate source (ADO or GitHub)
// and resolves the onboard tag patterns against them.
func resolveTagsForRepo(repoURL string, patterns []string) ([]string, error) {
	if isADORepo(repoURL) {
		tagInfos, err := ado.FetchAllTags(repoURL)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch tags for %s: %w", repoURL, err)
		}
		// ADO: every tag is tied to a commit, so all tags are valid candidates
		allTags := make([]string, len(tagInfos))
		for i, t := range tagInfos {
			allTags[i] = t.Name
		}
		return semver.ResolveOnboardTags(allTags, allTags, repoURL, patterns)
	}

	owner, repoName, _ := github.FetchRepositorySegments(repoURL)
	releaseTags, allTags, err := github.FetchAllTags(owner, repoName)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch tags for %s: %w", repoURL, err)
	}
	return semver.ResolveOnboardTags(releaseTags, allTags, repoURL, patterns)
}

