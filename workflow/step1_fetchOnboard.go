// ═══════════════════════════════════════════════════════════════════════════════
// Step 1 — Onboard
//
//   Reads onboard.yml files from the remote spec repo, resolves tag patterns
//   against the source repository (GitHub or ADO), and filters to only
//   actionable images/tags.
//
//   Chunk 1 · MAIN          FetchOnboardFiles()
//   Chunk 2 · TREE & CONFIG fetchRepoTree(), loadOnboardConfig(), fetchCachedSiblings()
//   Chunk 3 · TAG LOGIC     resolveAndBuildStates()
//   Chunk 4 · HELPERS       shouldProcessItem(), parseOnboardPath(), getExistingFilePaths()
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

// ─── Chunk 1 · MAIN ─────────────────────────────────────────────────────────

// FetchOnboardFiles reads partner-level onboard.yml files from the spec repo,
// flattens all components (standalone and grouped), resolves tags, fetches
// cached siblings, and returns a pipeline.State per (component, tag) pair.
func FetchOnboardFiles(inputPath string) ([]pipeline.State, error) {
	log.Printf("Full onboard search path: %s\n", inputPath)

	onboardItems, treePaths, err := fetchRepoTree(inputPath)
	if err != nil {
		return nil, err
	}

	var states []pipeline.State

	for _, item := range onboardItems {
		path, ok := shouldProcessItem(item, inputPath)
		if !ok {
			continue
		}
		log.Printf("Processing onboard file: %s\n", path)

		onboardDir, specRepository, err := parseOnboardPath(path)
		if err != nil {
			continue
		}

		components, err := loadOnboardConfig(path, onboardDir, specRepository)
		if err != nil {
			log.Printf("⚠️  %v\n", err)
			continue
		}

		for i := range components {
			onboard := &components[i]
			fetchCachedSiblings(onboard, treePaths)
			resolved := resolveAndBuildStates(onboard, treePaths)
			states = append(states, resolved...)
		}
	}
	return states, nil
}

// ─── Chunk 2 · TREE & CONFIG ─────────────────────────────────────────────────

// fetchRepoTree fetches the full recursive tree from the onboard repo and
// returns the raw tree items plus a path-lookup set. Logs only onboard files
// under the given inputPath prefix.
func fetchRepoTree(inputPath string) ([]interface{}, map[string]bool, error) {
	data, err := repository.FetchJSON(fmt.Sprintf(
		"repos/%s/%s/git/trees/%s?recursive=1",
		utils.OnboardOwner, utils.OnboardRepo, utils.OnboardBranch,
	))
	if err != nil {
		return nil, nil, fmt.Errorf("failed to fetch onboard data: %w", err)
	}
	items, ok := data["tree"].([]interface{})
	if !ok {
		return nil, nil, fmt.Errorf("unexpected response format: 'tree' field is missing or not an array")
	}

	treePaths := getExistingFilePaths(items)

	for _, item := range items {
		treeItem, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		itemPath, ok := treeItem["path"].(string)
		if !ok {
			continue
		}
		if strings.HasPrefix(itemPath, inputPath+"/") && strings.HasSuffix(itemPath, "/onboard.yml") {
			log.Printf("Discovered onboard file: %s\n", itemPath)
		}
	}

	return items, treePaths, nil
}

// loadOnboardConfig fetches a partner-level onboard.yml, unmarshals it into
// an OnboardFile, and flattens all components into a slice of ComponentConfig.
func loadOnboardConfig(path, onboardDir, specRepository string) ([]onboarding.ComponentConfig, error) {
	rawURL := fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/%s/%s",
		utils.OnboardOwner, utils.OnboardRepo, utils.OnboardBranch, path)
	content, err := repository.FetchRawContent(rawURL)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch onboard file %s: %w", path, err)
	}
	if len(content) == 0 {
		return nil, fmt.Errorf("skipping empty onboard file: %s", path)
	}

	var file onboarding.OnboardFile
	if err := yaml.Unmarshal(content, &file); err != nil {
		return nil, fmt.Errorf("failed to unmarshal %s: %w", path, err)
	}

	components := file.Flatten(onboardDir, specRepository)
	if len(components) == 0 {
		return nil, fmt.Errorf("no components found in %s", path)
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

// fetchCachedSiblings checks for and loads sibling Dockerfile/Makefile from the
// onboard repo. Returns true if at least one sibling exists (re-onboard scenario).
func fetchCachedSiblings(onboard *onboarding.ComponentConfig, treePaths map[string]bool) bool {
	siblingDF := onboard.OnboardDir + "/Dockerfile"
	siblingMF := onboard.OnboardDir + "/Makefile"
	hasDF := treePaths[siblingDF]
	hasMF := treePaths[siblingMF]
	if !hasDF && !hasMF {
		log.Printf("No sibling Dockerfile/Makefile found for %s — treating as first-time onboard\n", onboard.SpecImageName)
		return false
	}

	rawBase := fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/%s",
		utils.OnboardOwner, utils.OnboardRepo, utils.OnboardBranch)

	if hasDF {
		cachedDF, err := repository.FetchRawContent(rawBase + "/" + siblingDF)
		if err != nil {
			log.Printf("⚠️  Failed to fetch cached Dockerfile %s: %v\n", siblingDF, err)
		} else {
			onboard.DockerfileContent = cachedDF
		}
	}
	if hasMF {
		cachedMF, err := repository.FetchRawContent(rawBase + "/" + siblingMF)
		if err != nil {
			log.Printf("⚠️  Failed to fetch cached Makefile %s: %v\n", siblingMF, err)
		} else {
			onboard.MakefileContent = cachedMF
		}
	}

	log.Printf("Found existing siblings for %s (Dockerfile=%v, Makefile=%v) — will diff in Discover\n", onboard.SpecImageName, hasDF, hasMF)
	return true
}

// ─── Chunk 3 · TAG LOGIC ────────────────────────────────────────────────────

// resolveAndBuildStates resolves tag patterns, filters by existing specs and
// commit comparison, and returns a pipeline.State per (component, tag) pair.
// Tags that already have an up-to-date spec are skipped.
func resolveAndBuildStates(
	onboard *onboarding.ComponentConfig,
	treePaths map[string]bool,
) []pipeline.State {
	// ── Defaults ──
	if onboard.TagPatterns == nil {
		onboard.TagPatterns = []string{"latest"}
	}

	// ── Resolve and filter tags ──
	resolvedTags, err := semver.ResolveRepoTags(onboard.Repository, onboard.TagPatterns)
	if err != nil {
		log.Printf("⚠️  Failed to resolve tags for %s: %v\n", onboard.Repository, err)
		return nil
	}
	if len(resolvedTags) == 0 {
		log.Printf("Skipping %s: no tags matched patterns %v\n", onboard.SpecImageName, onboard.TagPatterns)
		return nil
	}
	log.Printf("✅ Resolved tags for %s: %v (from patterns: %v)\n",
		onboard.Repository, semver.TagNames(resolvedTags), onboard.TagPatterns)

	actionableTags := semver.FilterActionableTags(resolvedTags, onboard.SpecDir(), onboard.SpecImageName, utils.OnboardOwner, utils.OnboardRepo, utils.OnboardBranch, treePaths)
	if len(actionableTags) == 0 {
		log.Printf("Skipping %s: all tags already up to date\n", onboard.SpecImageName)
		return nil
	}

	// ── Build resolved tag sets ──
	tagSets := buildResolvedTagSets(actionableTags)
	onboard.ResolvedTags = tagSets

	// ── Expand into one State per tag ──
	states := make([]pipeline.State, len(tagSets))
	for i, tagSet := range tagSets {
		states[i] = pipeline.State{
			Onboard: onboard,
			Tag:     tagSet,
		}
	}
	return states
}

// ─── Chunk 4 · HELPERS ──────────────────────────────────────────────────────

// shouldProcessItem checks whether a tree item is an onboard.yml file under
// the given inputPath. Returns the file path and true if it should be processed.
func shouldProcessItem(item interface{}, inputPath string) (string, bool) {
	itemMap, ok := item.(map[string]interface{})
	if !ok {
		return "", false
	}
	path, _ := itemMap["path"].(string)
	if !strings.HasPrefix(path, inputPath+"/") && path != inputPath {
		return "", false
	}
	if !strings.HasSuffix(path, "onboard.yml") {
		return "", false
	}
	return path, true
}

// buildResolvedTagSets converts actionable tags into onboarding.TagSet entries.
func buildResolvedTagSets(actionableTags []semver.ActionableTag) []onboarding.TagSet {
	resolvedTagSets := make([]onboarding.TagSet, len(actionableTags))
	for i, actionableTag := range actionableTags {
		strippedTag := semver.ToTag(actionableTag.Name)
		resolvedTagSets[i] = onboarding.NewTagSet(actionableTag.Name, "", strippedTag, actionableTag.NextRevision)
	}
	return resolvedTagSets
}

// parseOnboardPath extracts the onboard directory and partner name from an
// onboard.yml path like "<prefix>/<partner>/onboard.yml".
// onboardDir maps to ComponentConfig.OnboardDir; specRepository maps to ComponentConfig.SpecRepository.
func parseOnboardPath(path string) (onboardDir, specRepository string, err error) {
	parts := strings.Split(path, "/")
	segmentCount := len(parts)
	if parts[segmentCount-1] != "onboard.yml" {
		return "", "", fmt.Errorf("not an onboard file: %s", path)
	}
	if segmentCount < 3 {
		return "", "", fmt.Errorf("unexpected file path format: %s (expected <prefix>/<partner>/onboard.yml)", path)
	}
	onboardDir = strings.Join(parts[:segmentCount-1], "/") // e.g. "specs/containernetworking"
	specRepository = parts[segmentCount-2]                  // e.g. "containernetworking"
	return onboardDir, specRepository, nil
}

// getExistingFilePaths builds a lookup set of all file paths in the repo tree.
func getExistingFilePaths(items []interface{}) map[string]bool {
	treePaths := make(map[string]bool)
	for _, item := range items {
		treeItem, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		itemPath, ok := treeItem["path"].(string)
		if !ok {
			continue
		}
		treePaths[itemPath] = true
	}
	return treePaths
}
