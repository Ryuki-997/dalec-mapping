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

func FetchOnboardFiles(onboardImages *[]onboarding.OnboardingInfo) error {
	url := utils.OnboardDirectory

	data, err := github.MakeGitHubRequest[map[string]interface{}](repository.GithubRequest{URL: url})
	if err != nil {
		return fmt.Errorf("failed to fetch onboard data: %w", err)
	}

	onboardItems, ok := data["tree"].([]interface{})
	if !ok {
		return fmt.Errorf("unexpected response format: 'tree' field is missing or not an array")
	}

	for _, item := range onboardItems {
		itemMap, ok := item.(map[string]interface{})
		if !ok {
			return fmt.Errorf("unexpected item format: expected a map")
		}

		path, ok := itemMap["path"].(string)
		if !ok {
			return fmt.Errorf("unexpected item format: 'path' field is missing or not a string")
		}

		parts := strings.Split(path, "/")
		n := len(parts)
		if parts[n-1] != "onboard.yml" {
			continue
		}

		var specRepository, specImageName string

		switch n {
		case 4: 
			specRepository, specImageName = parts[0], parts[1]
		case 3: 
			specRepository, specImageName = "", parts[1]
		default:
			return fmt.Errorf("unexpected file path format: %s", path)
		}

	contentsURL := fmt.Sprintf("https://api.github.com/repos/azure-management-and-platforms/aks-dalec-build-defs/contents/%s?ref=ksehgal/fix-publish-poc", path)

	fileData, err := github.MakeGitHubRequest[map[string]interface{}](repository.GithubRequest{URL: contentsURL})
	if err != nil {
		return fmt.Errorf("failed to fetch onboard file %s: %w", path, err)
	}

	contentStr, ok := fileData["content"].(string)
	if !ok {
		return fmt.Errorf("unexpected response: missing or invalid content field")
	}

	if len(contentStr) == 0 {
		fmt.Printf("⚠️  Skipping empty onboard file: %s\n", path)
		continue
	}

	content, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(contentStr, "\n", ""))
	if err != nil {
		return fmt.Errorf("failed to decode base64 content: %w", err)
		}

		onboard := onboarding.OnboardingInfo{
			Repository: "",
			Tag:        []string{},
			Dockerfile: "",
			Makefile:   "",
			SpecImageName: specImageName,
			SpecRepository: specRepository,
		}

		fmt.Printf("Data: %s\n", string(content))

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

		if len(resolvedTags) > 0 {
			onboard.Tag = resolvedTags
			*onboardImages = append(*onboardImages, onboard)
		}

		fmt.Printf("Onboard Data: %v\n", onboard)
	}
	return nil
}
