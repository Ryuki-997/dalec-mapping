package tool

import (
	"dalec-mapping/github"
	"dalec-mapping/global"
	"encoding/base64"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

func Fetch(onboardFiles *[]global.OnboardingInfo) error {
	url := global.OnboardDirectory

	data, err := global.MakeGitHubRequest[map[string]interface{}](global.GithubRequest{URL: url})
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

	contentsURL := fmt.Sprintf("https://api.github.com/repos/azure-management-and-platforms/aks-dalec-build-defs/contents/%s?ref=ksehgal/fix-publish-poc", path)

	fileData, err := global.MakeGitHubRequest[map[string]interface{}](global.GithubRequest{URL: contentsURL})
	if err != nil {
		return fmt.Errorf("failed to fetch onboard file %s: %w", path, err)
	}

	contentStr, ok := fileData["content"].(string)
	if !ok {
		return fmt.Errorf("unexpected response: missing or invalid content field")
	}
	content, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(contentStr, "\n", ""))
	if err != nil {
		return fmt.Errorf("failed to decode base64 content: %w", err)
		}

		onboard := global.OnboardingInfo{
			Repository: "",
			Tag:        "",
			Dockerfile: []string{},
			Makefile:   []string{},
		}

		fmt.Printf("Data: %s\n", string(content))

		if err := yaml.Unmarshal(content, &onboard); err != nil {
			return fmt.Errorf("failed to unmarshal onboard data: %w", err)
		}

		if onboard.Tag == "" {
			repoInfo, err := github.FetchRepoInfo(onboard.Repository, "")
			if err == nil && repoInfo.Version != "" {
				onboard.Tag = repoInfo.Version
				fmt.Printf("Using latest release tag: %s\n", onboard.Tag)
			}
		}

		fmt.Printf("Onboard Data: %v\n", onboard)
		*onboardFiles = append(*onboardFiles, onboard)
	}
	return nil
}