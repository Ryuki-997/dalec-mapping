package workflow

import (
	"encoding/base64"
	"fmt"
	"strings"

	"dalec-mapping/domain/onboarding"
	"dalec-mapping/domain/repository"
	"dalec-mapping/infrastructure/github"
	"dalec-mapping/infrastructure/semver"
	"dalec-mapping/utils"

	"gopkg.in/yaml.v3"
)

func FetchOnboardFiles(onboardImages *[]onboarding.OnboardingInfo, inputPath string) error {
	// Build path prefix for filtering: always rooted under "specs/"
	if inputPath != "" {
		inputPath = "specs/" + inputPath
	}

	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/git/trees/%s?recursive=1", utils.OnboardOwner, utils.OnboardRepo, utils.OnboardBranch)

	data, err := github.MakeGitHubRequest[map[string]interface{}](repository.GithubRequest{URL: url})
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

		specRepository, specImageName, err := getOnboardFilepath(path)
		if err != nil {
			continue
		}

		content, err := getOnboardFileContent(path)
		if err != nil {
			return fmt.Errorf("failed to fetch onboard file %s: %w", path, err)
		}

		if len(content) == 0 {
			fmt.Printf("⚠️  Skipping empty onboard file: %s\n", path)
			continue
		}

		onboard := onboarding.OnboardingInfo{
			Repository: "",
			Tag:        []string{},
			Dockerfile: "",
			Makefile:   "",
			SpecImageName: specImageName,
			SpecRepository: specRepository,
		}

		if err := yaml.Unmarshal(content, &onboard); err != nil {
			return fmt.Errorf("failed to unmarshal onboard data: %w", err)
		}
		
		if onboard.Tag == nil {
			onboard.Tag = append(onboard.Tag, "latest")
		}

		resolvedTags, err := semver.ResolveOnboardTags(onboard.Repository, onboard.Tag)
		if err != nil {
			fmt.Printf("⚠️  Failed to resolve tags for %s: %v\n", onboard.Repository, err)
			continue
		}
		fmt.Printf("✅ Resolved tags for %s: %v (from patterns: %v)\n", onboard.Repository, resolvedTags, onboard.Tag)

		// Filter out resolved tags whose spec files already exist in the remote repo
		filteredTags := getFilteredTags(resolvedTags, onboard.SpecRepository, onboard.SpecImageName, treePaths)

		if len(filteredTags) > 0 {
			onboard.Tag = filteredTags
			*onboardImages = append(*onboardImages, onboard)
		}

		fmt.Printf("Onboard Data: %v\n", onboard)
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

func getOnboardFileContent(path string) ([]byte, error) {
	contentsURL := fmt.Sprintf("https://api.github.com/repos/%s/%s/contents/%s?ref=%s",utils.OnboardOwner, utils.OnboardRepo, path, utils.OnboardBranch)

	fileData, err := github.MakeGitHubRequest[map[string]interface{}](repository.GithubRequest{URL: contentsURL})
	if err != nil {
		return nil, fmt.Errorf("failed to fetch onboard file %s: %w", path, err)
	}

	contentStr, ok := fileData["content"].(string)
	if !ok {
		return nil, fmt.Errorf("unexpected response: missing or invalid content field")
	}

	content, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(contentStr, "\n", ""))
	if err != nil {
		return nil, fmt.Errorf("failed to decode base64 content: %w", err)
	}
	return content, nil
}

func getFilteredTags(resolvedTags []string, specRepo, specImage string, existingPaths map[string]bool) []string {
	var filteredTags []string
	for _, tag := range resolvedTags {
		var specPath string
		if specRepo != "" {
			specPath = fmt.Sprintf("specs/%s/%s/%s-%s-specfile.yml", specRepo, specImage, specImage, tag)
		} else {
			specPath = fmt.Sprintf("specs/%s/%s-%s-specfile.yml", specImage, specImage, tag)

		}
		if existingPaths[specPath] {
			fmt.Printf("⏭  Skipping %s @ %s: spec file already exists at %s\n", specImage, tag, specPath)
			continue
		}
		filteredTags = append(filteredTags, tag)
	}
	return filteredTags
}