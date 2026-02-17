package tool

import (
	"dalec-mapping/global"
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

		rawURL := fmt.Sprintf("https://raw.githubusercontent.com/azure-management-and-platforms/aks-dalec-build-defs/ksehgal/fix-publish-poc/%s", path)

		content, err := global.FetchRawContent(rawURL)
		if err != nil {
			return fmt.Errorf("failed to fetch onboard file %s: %w", path, err)
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

		fmt.Printf("Onboard Data: %v\n", onboard)
		*onboardFiles = append(*onboardFiles, onboard)
	}
	return nil
}