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

// FetchOnboardFiles reads onboard.yml files from the spec repo, resolves tags,
// fetches cached siblings, and populates the onboardImages slice.
func FetchOnboardFiles(onboardImages *[]onboarding.OnboardingInfo, isFirstOnboard *[]bool, templateTags *[]string, inputPath string) error {
	if inputPath == "" {
		inputPath = "specs"
	} else {
		inputPath = "specs/" + inputPath
	}

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
		if !strings.HasPrefix(path, inputPath) {
			continue
		}

		specRepository, specImageName, err := getOnboardFilepath(path)
		if err != nil {
			continue
		}

		onboard, err := loadOnboardConfig(path, specRepository, specImageName)
		if err != nil {
			log.Printf("⚠️  %v\n", err)
			continue
		}

		hasSiblings := fetchCachedSiblings(&onboard, treePaths)

		resolveAndAppend(&onboard, hasSiblings, treePaths, onboardImages, isFirstOnboard, templateTags)
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
			if p, ok := m["path"].(string); ok && strings.HasPrefix(p, inputPath) && strings.HasSuffix(p, "/onboard.yml") {
				log.Printf("📋 Discovered onboard file: %s\n", p)
			}
		}
	}

	return items, treePaths, nil
}

// loadOnboardConfig fetches and unmarshals a single onboard.yml from the remote repo.
func loadOnboardConfig(path, specRepository, specImageName string) (onboarding.OnboardingInfo, error) {
	rawURL := fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/%s/%s",
		utils.OnboardOwner, utils.OnboardRepo, utils.OnboardBranch, path)
	content, err := repository.FetchRawContent(rawURL)
	if err != nil {
		return onboarding.OnboardingInfo{}, fmt.Errorf("failed to fetch onboard file %s: %w", path, err)
	}
	if len(content) == 0 {
		return onboarding.OnboardingInfo{}, fmt.Errorf("skipping empty onboard file: %s", path)
	}

	onboard := onboarding.OnboardingInfo{
		Tag:            []string{},
		SpecImageName:  specImageName,
		SpecRepository: specRepository,
		OnboardDir:     path[:strings.LastIndex(path, "/")],
	}
	if err := yaml.Unmarshal(content, &onboard); err != nil {
		return onboarding.OnboardingInfo{}, fmt.Errorf("failed to unmarshal %s: %w", path, err)
	}

	log.Printf("Onboard Data: %v\n", onboard)
	return onboard, nil
}

// fetchCachedSiblings checks for and loads sibling Dockerfile/Makefile from the
// onboard repo. Returns true if both siblings exist (re-onboard scenario).
func fetchCachedSiblings(onboard *onboarding.OnboardingInfo, treePaths map[string]bool) bool {
	siblingDF := onboard.OnboardDir + "/Dockerfile"
	siblingMF := onboard.OnboardDir + "/Makefile"
	if !treePaths[siblingDF] || !treePaths[siblingMF] {
		log.Printf("No sibling Dockerfile/Makefile found for %s — treating as first-time onboard\n", onboard.SpecImageName)
		return false
	}

	rawBase := fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/%s",
		utils.OnboardOwner, utils.OnboardRepo, utils.OnboardBranch)

	cachedDF, err := repository.FetchRawContent(rawBase + "/" + siblingDF)
	if err != nil {
		log.Printf("⚠️  Failed to fetch cached Dockerfile %s: %v\n", siblingDF, err)
		return false
	}
	cachedMF, err := repository.FetchRawContent(rawBase + "/" + siblingMF)
	if err != nil {
		log.Printf("⚠️  Failed to fetch cached Makefile %s: %v\n", siblingMF, err)
		return false
	}

	onboard.DockerfileContent = cachedDF
	onboard.MakefileContent = cachedMF
	log.Printf("📂 Found existing siblings for %s — will diff in Discover\n", onboard.SpecImageName)
	return true
}

// ─── Chunk 3 · TAG LOGIC ────────────────────────────────────────────────────

// resolveAndAppend resolves tag patterns, filters by existing specs, and
// appends the onboard entry to the output slices.
func resolveAndAppend(
	onboard *onboarding.OnboardingInfo,
	hasSiblings bool,
	treePaths map[string]bool,
	onboardImages *[]onboarding.OnboardingInfo,
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
	log.Printf("✅ Resolved tags for %s: %v (from patterns: %v)\n",
		onboard.Repository, semver.TagNames(resolvedTags), onboard.Tag)

	newTags := semver.FilterNewTags(resolvedTags, onboard.SpecRepository, onboard.SpecImageName, treePaths)

	if hasSiblings {
		// Re-onboard: need both new tags and an existing spec as template
		existing := semver.FilterExistingTags(resolvedTags, onboard.SpecRepository, onboard.SpecImageName, treePaths)
		if len(newTags) == 0 || len(existing) == 0 {
			log.Printf("⏭  Skipping %s: re-onboard but no actionable tags (new=%d, existing=%d)\n",
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

// getOnboardFilepath extracts specRepository and specImageName from an
// onboard.yml path like "specs/<repo>/<image>/onboard.yml".
func getOnboardFilepath(path string) (string, string, error) {
	parts := strings.Split(path, "/")
	n := len(parts)
	if parts[n-1] != "onboard.yml" {
		return "", "", fmt.Errorf("not an onboard file: %s", path)
	}
	switch n {
	case 4:
		return parts[1], parts[2], nil
	case 3:
		return "", parts[1], nil
	default:
		return "", "", fmt.Errorf("unexpected file path format: %s", path)
	}
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
