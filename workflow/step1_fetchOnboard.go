// ═══════════════════════════════════════════════════════════════════════════════
// Step 1 — Onboard
//
//   Reads onboard.yml files from the remote spec repo, resolves tag patterns
//   against the source repository (GitHub or ADO), and filters to only
//   actionable images/tags.
//
//   Functions are ordered by call sequence:
//     FetchOnboardStates()
//       → fetchSpecRepoTree()
//       → resolveOnboardFiles()
//           → filterOnboardFile()
//           → splitOnboardPath()
//           → fetchOnboardFile()
//           → matchTagPatterns()
// ═══════════════════════════════════════════════════════════════════════════════

package workflow

import (
	"fmt"
	"log"
	"strings"

	"dalec-mapping/domain/onboarding"
	"dalec-mapping/infrastructure/repository"
	"dalec-mapping/infrastructure/semver"
	"dalec-mapping/pipeline"
	"dalec-mapping/utils"

	"gopkg.in/yaml.v3"
)

// FetchOnboardStates reads partner-level onboard.yml files from the spec repo,
// flattens all components (standalone and grouped), resolves tags (fetching
// once per unique repo), and returns a pipeline.State per (component, tag) pair.
func FetchOnboardStates(inputPath string) ([]pipeline.State, error) {
	log.Printf("Full onboard search path: %s\n", inputPath)

	specRepoEntries, existingPaths, err := fetchSpecRepoTree()
	if err != nil {
		return nil, err
	}

	return resolveOnboardFiles(specRepoEntries, existingPaths, inputPath)
}

// fetchSpecRepoTree fetches the full recursive tree from the spec repo and
// returns the raw tree entries plus a path-lookup set.
func fetchSpecRepoTree() ([]interface{}, map[string]bool, error) {
	data, err := repository.FetchJSON(fmt.Sprintf(
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
// found under inputPath, it fetches and flattens the file, then immediately
// resolves each component's tags into pipeline states.
func resolveOnboardFiles(specRepoEntries []interface{}, existingPaths map[string]bool, inputPath string) ([]pipeline.State, error) {
	tagCache := make(map[string]map[string]string) // repoURL → tagName → commitHash
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
			component := &components[i]

			repoURL := component.Repository
			if _, cached := tagCache[repoURL]; !cached {
				repoTags, err := semver.FetchRepoTags(repoURL)
				if err != nil {
					log.Printf("⚠️  Failed to fetch tags for %s: %v\n", repoURL, err)
					tagCache[repoURL] = make(map[string]string)
					continue
				}
				log.Printf("✅ Fetched %d tags for %s\n", len(repoTags), repoURL)
				tagCache[repoURL] = repoTags
			}

			repoTags := tagCache[repoURL]
			if len(repoTags) == 0 {
				continue
			}

			states = append(states, matchTagPatterns(component, repoTags, existingPaths)...)
		}
	}
	log.Println()
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
	onboardFileURL := fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/%s/%s",
		utils.OnboardOwner, utils.OnboardRepo, utils.OnboardBranch, onboardPath)
	rawContent, err := repository.FetchRawContent(onboardFileURL)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch onboard file %s: %w", onboardPath, err)
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
		log.Println()
	}
	return components, nil
}



// matchTagPatterns applies the component's tag patterns against the pre-fetched
// repo tags, constructs a pipeline.State for each actionable match, and returns them.
func matchTagPatterns(component *onboarding.ComponentConfig, repoTags map[string]string, existingPaths map[string]bool) []pipeline.State {
	tagPatterns := component.TagPatterns
	if tagPatterns == nil {
		tagPatterns = []string{"latest"}
	}

	actionableTags := semver.MatchTagSets(repoTags, tagPatterns, component.SpecDir(), component.SpecImageName, existingPaths)
	if len(actionableTags) == 0 {
		log.Printf("Skipping %s: no actionable tags matched patterns %v\n", component.SpecImageName, tagPatterns)
		return nil
	}

	var states []pipeline.State
	for _, actionableTag := range actionableTags {
		strippedTag := semver.ToTag(actionableTag.Name)
		tagSet := onboarding.NewTagSet(actionableTag.Name, "", strippedTag, actionableTag.NextRevision)
		states = append(states, pipeline.State{
			Onboard: component,
			Tag:     tagSet,
		})
		log.Printf("Queued: %s @ %s (R%d)\n", component.SpecImageName, actionableTag.Name, actionableTag.NextRevision)
	}
	return states
}
