// ═══════════════════════════════════════════════════════════════════════════════
// Step 1 — Onboard
//
//   Reads onboard.yml files from the remote spec repo and flattens all
//   components (standalone and grouped) into one pipeline.State per component.
//   Tag resolution is deferred to step 2.
//
//   Functions are ordered by call sequence:
//     FetchOnboardStates()
//       → fetchSpecRepoTree()
//       → resolveOnboardFiles()
//           → filterOnboardFile()
//           → splitOnboardPath()
//           → fetchOnboardFile()
// ═══════════════════════════════════════════════════════════════════════════════

package workflow

import (
	"encoding/base64"
	"fmt"
	"log"
	"strings"

	"dalec-mapping/domain/onboarding"
	"dalec-mapping/infrastructure/github"
	"dalec-mapping/pipeline"
	"dalec-mapping/utils"

	"gopkg.in/yaml.v3"
)

// FetchOnboardStates reads partner-level onboard.yml files from the spec repo
// and flattens all components (standalone and grouped) into one pipeline.State
// per component. Returns the component states and the set of existing file
// paths in the spec repo (used by step 2 for revision calculation).
func FetchOnboardStates(inputPath string) ([]pipeline.State, map[string]bool, error) {
	log.Printf("Full onboard search path: %s\n", inputPath)

	specRepoEntries, existingPaths, err := fetchSpecRepoTree()
	if err != nil {
		return nil, nil, err
	}

	states, err := resolveOnboardFiles(specRepoEntries, inputPath)
	if err != nil {
		return nil, nil, err
	}

	log.Printf("Step 1 output: %d components loaded, %d existing paths indexed\n", len(states), len(existingPaths))
	return states, existingPaths, nil
}

// fetchSpecRepoTree fetches the full recursive tree from the spec repo and
// returns the raw tree entries plus a path-lookup set.
func fetchSpecRepoTree() ([]interface{}, map[string]bool, error) {
	data, err := github.FetchJSON(fmt.Sprintf(
		"repos/%s/%s/git/trees/%s?recursive=1",
		utils.OnboardOwner, utils.OnboardRepo, utils.OnboardBranch,
	))
	if err != nil {
		return nil, nil, fmt.Errorf("failed to fetch onboard data: %w", err)
	}
	treeEntries, ok := data["tree"].([]interface{})
	if !ok {
		return nil, nil, fmt.Errorf("unexpected response format: 'tree' field is missing or not an array")
	}

	existingPaths := buildPathIndex(treeEntries)

	return treeEntries, existingPaths, nil
}

// buildPathIndex builds a lookup set of all file paths in the repo tree.
func buildPathIndex(treeEntries []interface{}) map[string]bool {
	pathIndex := make(map[string]bool)
	for _, entry := range treeEntries {
		entryMap, ok := entry.(map[string]interface{})
		if !ok {
			continue
		}
		entryPath, ok := entryMap["path"].(string)
		if !ok {
			continue
		}
		pathIndex[entryPath] = true
	}
	return pathIndex
}

// resolveOnboardFiles iterates the spec repo tree entries. For each onboard.yml
// found under inputPath, it fetches and flattens the file into one pipeline.State
// per component (Tag, RepoInfo, etc. are left empty for later steps).
func resolveOnboardFiles(specRepoEntries []interface{}, inputPath string) ([]pipeline.State, error) {
	var states []pipeline.State

	for _, entry := range specRepoEntries {
		onboardPath, ok := filterOnboardFile(entry, inputPath)
		if !ok {
			continue
		}
		log.Println()
		log.Printf("Processing onboard file: %s\n", onboardPath)

		onboardDir, specRepository, err := splitOnboardPath(onboardPath)
		if err != nil {
			continue
		}

		components, err := fetchOnboardFile(onboardPath, onboardDir, specRepository)
		if err != nil {
			log.Printf("⚠️  %v\n", err)
			continue
		}

		log.Println()
		for i := range components {
			component := components[i]
			states = append(states, pipeline.State{ComponentState: onboarding.ComponentState{Onboard: &component}})
		}
	}
	return states, nil
}

// filterOnboardFile checks whether a tree entry is an onboard.yml file under
// the given inputPath. Returns the file path and true if it should be processed.
func filterOnboardFile(entry interface{}, inputPath string) (string, bool) {
	entryMap, ok := entry.(map[string]interface{})
	if !ok {
		return "", false
	}
	entryPath, _ := entryMap["path"].(string)
	if !strings.HasPrefix(entryPath, inputPath+"/") && entryPath != inputPath {
		return "", false
	}
	if !strings.HasSuffix(entryPath, "/onboard.yml") {
		return "", false
	}
	return entryPath, true
}

// splitOnboardPath extracts the onboard directory and partner name from an
// onboard.yml path like "<prefix>/<partner>/onboard.yml".
// onboardDir maps to ComponentConfig.OnboardDir; specRepository maps to ComponentConfig.SpecRepository.
func splitOnboardPath(onboardPath string) (onboardDir, specRepository string, err error) {
	segments := strings.Split(onboardPath, "/")
	segmentCount := len(segments)
	if segmentCount < 3 {
		return "", "", fmt.Errorf("unexpected file path format: %s (expected <prefix>/<partner>/onboard.yml)", onboardPath)
	}
	onboardDir = strings.Join(segments[:segmentCount-1], "/") // e.g. "specs/containernetworking"
	specRepository = segments[segmentCount-2]                  // e.g. "containernetworking"
	return onboardDir, specRepository, nil
}

// fetchOnboardFile fetches a partner-level onboard.yml, unmarshals it into
// an OnboardFile, and flattens all components into a slice of ComponentConfig.
func fetchOnboardFile(onboardPath, onboardDir, specRepository string) ([]onboarding.ComponentConfig, error) {
	contentsPath := fmt.Sprintf("repos/%s/%s/contents/%s?ref=%s",
		utils.OnboardOwner, utils.OnboardRepo, onboardPath, utils.OnboardBranch)
	data, err := github.FetchJSON(contentsPath)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch onboard file %s: %w", onboardPath, err)
	}

	encodedContent, ok := data["content"].(string)
	if !ok {
		return nil, fmt.Errorf("no content field in response for %s", onboardPath)
	}

	rawContent, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(encodedContent, "\n", ""))
	if err != nil {
		return nil, fmt.Errorf("failed to decode base64 content for %s: %w", onboardPath, err)
	}

	if len(rawContent) == 0 {
		return nil, fmt.Errorf("skipping empty onboard file: %s", onboardPath)
	}

	var onboardFile onboarding.OnboardFile
	if err := yaml.Unmarshal(rawContent, &onboardFile); err != nil {
		return nil, fmt.Errorf("failed to unmarshal %s: %w", onboardPath, err)
	}

	components := onboardFile.Flatten(onboardDir, specRepository)
	if len(components) == 0 {
		return nil, fmt.Errorf("no components found in %s", onboardPath)
	}

	for _, component := range components {
		if component.SpecRepository != "" {
			log.Printf("Onboard Data: %s/%s repo=%s tags=%v\n", component.SpecRepository, component.SpecImageName, component.Repository, component.TagPatterns)
		} else {
			log.Printf("Onboard Data: %s repo=%s tags=%v\n", component.SpecImageName, component.Repository, component.TagPatterns)
		}
	}
	return components, nil
}
