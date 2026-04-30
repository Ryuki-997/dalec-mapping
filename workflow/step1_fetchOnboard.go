// ═══════════════════════════════════════════════════════════════════════════════
// Step 1 — Onboard
//
//   Reads onboard.yml files from the remote spec repo, resolves tag patterns
//   against the source repository (GitHub or ADO), and filters to only
//   actionable images/tags.
//
//   Chunk 1 · MAIN          FetchOnboardFiles()
//   Chunk 2 · TREE & CONFIG fetchRepoTree(), loadOnboardConfig(), fetchCachedSiblings()
//   Chunk 3 · TAG LOGIC     resolveAndAppend()
//   Chunk 4 · UTILITIES     getOnboardFilepath(), getExistingFilePaths()
// ═══════════════════════════════════════════════════════════════════════════════

package workflow

import (
	"fmt"
	"log"
	"strings"

	"dalec-mapping/domain/onboarding"
	"dalec-mapping/infrastructure/repository"
	"dalec-mapping/infrastructure/semver"
	"dalec-mapping/utils"

	"gopkg.in/yaml.v3"
)

// ─── Chunk 1 · MAIN ─────────────────────────────────────────────────────────

// FetchOnboardFiles reads partner-level onboard.yml files from the spec repo,
// flattens all components (standalone and grouped), resolves tags, fetches
// cached siblings, and populates the onboardImages slice.
func FetchOnboardFiles(onboardImages *[]onboarding.ComponentConfig, isFirstOnboard *[]bool, templateTags *[]string, inputPath string) error {
	log.Printf("Full onboard search path: %s\n", inputPath)

	onboardItems, treePaths, err := fetchRepoTree(inputPath)
	if err != nil {
		return err
	}

	for _, item := range onboardItems {
		itemMap, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		path, _ := itemMap["path"].(string)
		if !strings.HasPrefix(path, inputPath+"/") && path != inputPath {
			continue
		}
		if !strings.HasSuffix(path, "onboard.yml") {
			continue
		}
		log.Printf("Processing onboard file: %s\n", path)

		onboardParentDir, specRepository, err := getOnboardFilepath(path)
		if err != nil {
			continue
		}

		components, err := loadOnboardConfig(path, onboardParentDir, specRepository)
		if err != nil {
			log.Printf("⚠️  %v\n", err)
			continue
		}

		for i := range components {
			onboard := &components[i]
			hasSiblings := fetchCachedSiblings(onboard, treePaths)
			resolveAndAppend(onboard, hasSiblings, treePaths, onboardImages, isFirstOnboard, templateTags)
		}
	}
	return nil
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
		if m, ok := item.(map[string]interface{}); ok {
			if p, ok := m["path"].(string); ok && strings.HasPrefix(p, inputPath+"/") && strings.HasSuffix(p, "/onboard.yml") {
				log.Printf("Discovered onboard file: %s\n", p)
			}
		}
	}

	return items, treePaths, nil
}

// loadOnboardConfig fetches a partner-level onboard.yml, unmarshals it into
// an OnboardFile, and flattens all components into a slice of ComponentConfig.
func loadOnboardConfig(path, onboardParentDir, specRepository string) ([]onboarding.ComponentConfig, error) {
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

	components := file.Flatten(onboardParentDir, specRepository)
	if len(components) == 0 {
		return nil, fmt.Errorf("no components found in %s", path)
	}

	for _, c := range components {
		if c.SpecRepository != "" {
			log.Printf("Onboard Data: %s/%s repo=%s tags=%v\n", c.SpecRepository, c.SpecImageName, c.Repository, c.Tag)
		} else {
			log.Printf("Onboard Data: %s repo=%s tags=%v\n", c.SpecImageName, c.Repository, c.Tag)
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

// resolveAndAppend resolves tag patterns, filters by existing specs, and
// appends the onboard entry to the output slices.
func resolveAndAppend(
	onboard *onboarding.ComponentConfig,
	hasSiblings bool,
	treePaths map[string]bool,
	onboardImages *[]onboarding.ComponentConfig,
	isFirstOnboard *[]bool,
	templateTags *[]string,
) {
	if onboard.Tag == nil {
		onboard.Tag = []string{"latest"}
	}

	resolvedTags, err := semver.ResolveRepoTags(onboard.Repository, onboard.Tag)
	if err != nil {
		log.Printf("⚠️  Failed to resolve tags for %s: %v\n", onboard.Repository, err)
		return
	}
	if len(resolvedTags) == 0 {
		log.Printf("Skipping %s: no release tags matched patterns %v\n", onboard.SpecImageName, onboard.Tag)
		return
	}
	log.Printf("✅ Resolved tags for %s: %v (from patterns: %v)\n",
		onboard.Repository, semver.TagNames(resolvedTags), onboard.Tag)

	newTags := semver.FilterNewTags(resolvedTags, onboard.SpecDir(), onboard.SpecImageName, treePaths)

	if hasSiblings {
		// Re-onboard: need both new tags and an existing spec as template
		existing := semver.FilterExistingTags(resolvedTags, onboard.SpecDir(), onboard.SpecImageName, treePaths)
		if len(newTags) == 0 || len(existing) == 0 {
			log.Printf("Skipping %s: re-onboard but no actionable tags (new=%d, existing=%d)\n",
				onboard.SpecImageName, len(newTags), len(existing))
			return
		}
		onboard.Tag = semver.TagNames(newTags)
		*onboardImages = append(*onboardImages, *onboard)
		*isFirstOnboard = append(*isFirstOnboard, false)
		*templateTags = append(*templateTags, existing[len(existing)-1].Name)
		return
	}

	// First-time onboard
	if len(newTags) > 0 {
		onboard.Tag = semver.TagNames(newTags)
	} else {
		// All tags already have specs — re-process the latest
		log.Printf("⚠️  All resolved tags for %s already have specs — re-processing latest tag\n", onboard.SpecImageName)
		onboard.Tag = []string{resolvedTags[len(resolvedTags)-1].Name}
	}
	*onboardImages = append(*onboardImages, *onboard)
	*isFirstOnboard = append(*isFirstOnboard, true)
	*templateTags = append(*templateTags, "")
}

// ─── Chunk 4 · UTILITIES ────────────────────────────────────────────────────

// getOnboardFilepath extracts the parent directory and partner name from an
// onboard.yml path like "<prefix>/<partner>/onboard.yml".
// Returns (parentDir, partnerName, error).
func getOnboardFilepath(path string) (string, string, error) {
	parts := strings.Split(path, "/")
	n := len(parts)
	if parts[n-1] != "onboard.yml" {
		return "", "", fmt.Errorf("not an onboard file: %s", path)
	}
	// Need at least <prefix>/<partner>/onboard.yml (3+ segments)
	if n < 3 {
		return "", "", fmt.Errorf("unexpected file path format: %s (expected <prefix>/<partner>/onboard.yml)", path)
	}
	parentDir := strings.Join(parts[:n-1], "/") // e.g. "specs/containernetworking"
	partnerName := parts[n-2]                   // e.g. "containernetworking"
	return parentDir, partnerName, nil
}

// getExistingFilePaths builds a lookup set of all file paths in the repo tree.
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
