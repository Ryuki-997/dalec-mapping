// ═══════════════════════════════════════════════════════════════════════════════
// Step 1 — Onboard
//
//   Reads onboard.yml files from the remote spec repo, resolves tag patterns
//   against the source repository (GitHub or ADO), and filters to only
//   actionable images/tags.
//
//   Chunk 1 · MAIN          FetchOnboardFiles()
//   Chunk 2 · TREE PARSING  getOnboardFilepath(), getExistingFilePaths()
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
	// Normalise the search path
	if inputPath == "" {
		inputPath = "specs"
	} else {
		inputPath = "specs/" + inputPath
	}

	// Fetch the full repo tree to discover onboard.yml paths and check spec existence
	data, err := repository.FetchJSON(fmt.Sprintf("repos/%s/%s/git/trees/%s?recursive=1", utils.OnboardOwner, utils.OnboardRepo, utils.OnboardBranch))
	if err != nil {
		return fmt.Errorf("failed to fetch onboard data: %w", err)
	}
	onboardItems, ok := data["tree"].([]interface{})
	if !ok {
		return fmt.Errorf("unexpected response format: 'tree' field is missing or not an array")
	}

	// Build a set of all existing file paths for duplicate/spec-existence checks
	treePaths := getExistingFilePaths(onboardItems)

	// Iterate each tree item and process matching onboard.yml files
	for _, item := range onboardItems {
		itemMap, ok := item.(map[string]interface{})
		if !ok {
			return fmt.Errorf("unexpected item format: expected a map")
		}
		path, ok := itemMap["path"].(string)
		if !ok {
			return fmt.Errorf("unexpected item format: 'path' field is missing or not a string")
		}
		if !strings.HasPrefix(path, inputPath) {
			continue
		}
		log.Printf("Found file in repo: %s\n", path)

		// Extract specRepository / specImageName from the path
		specRepository, specImageName, err := getOnboardFilepath(path)
		if err != nil {
			continue
		}

		// Fetch and unmarshal the onboard.yml content
		rawURL := fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/%s/%s", utils.OnboardOwner, utils.OnboardRepo, utils.OnboardBranch, path)
		content, err := repository.FetchRawContent(rawURL)
		if err != nil {
			return fmt.Errorf("failed to fetch onboard file %s: %w", path, err)
		}
		if len(content) == 0 {
			log.Printf("⚠️  Skipping empty onboard file: %s\n", path)
			continue
		}

		onboard := onboarding.OnboardingInfo{
			Tag:            []string{},
			SpecImageName:  specImageName,
			SpecRepository: specRepository,
			OnboardDir:     path[:strings.LastIndex(path, "/")],
		}
		if err := yaml.Unmarshal(content, &onboard); err != nil {
			return fmt.Errorf("failed to unmarshal onboard data: %w", err)
		}

		// Check for cached sibling Dockerfile/Makefile (indicates previous onboard)
		siblingDockerfile := onboard.OnboardDir + "/Dockerfile"
		siblingMakefile := onboard.OnboardDir + "/Makefile"
		hasDockerfile := treePaths[siblingDockerfile]
		hasMakefile := treePaths[siblingMakefile]

		if !hasDockerfile || !hasMakefile {
			log.Printf("No sibling Dockerfile/Makefile found for %s — treating as first-time onboard\n", specImageName)
		} else {
			// Fetch cached siblings for diff comparison in Discover
			rawBase := fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/%s", utils.OnboardOwner, utils.OnboardRepo, utils.OnboardBranch)
			cachedDF, err := repository.FetchRawContent(rawBase + "/" + siblingDockerfile)
			if err != nil {
				return fmt.Errorf("failed to fetch cached Dockerfile %s: %w", siblingDockerfile, err)
			}
			cachedMF, err := repository.FetchRawContent(rawBase + "/" + siblingMakefile)
			if err != nil {
				return fmt.Errorf("failed to fetch cached Makefile %s: %w", siblingMakefile, err)
			}
			onboard.DockerfileContent = cachedDF
			onboard.MakefileContent = cachedMF
			log.Printf("📂 Found existing Dockerfile and Makefile siblings for %s — will diff in Discover\n", specImageName)
		}

		// Default to "latest" when no tags are specified
		if onboard.Tag == nil {
			onboard.Tag = append(onboard.Tag, "latest")
		}

		// Resolve tag patterns against the source repo
		resolvedTags, err := semver.ResolveRepoTags(onboard.Repository, onboard.Tag)
		if err != nil {
			log.Printf("⚠️  Failed to resolve tags for %s: %v\n", onboard.Repository, err)
			continue
		}
		log.Printf("✅ Resolved tags for %s: %v (from patterns: %v)\n", onboard.Repository, semver.TagNames(resolvedTags), onboard.Tag)

		// Decide which tags to keep based on first-time vs re-onboard
		filteredTags := semver.FilterNewTags(resolvedTags, onboard.SpecRepository, onboard.SpecImageName, treePaths)

		if hasDockerfile && hasMakefile {
			// Re-onboard: process new tags using the latest existing spec as template
			existingTags := semver.FilterExistingTags(resolvedTags, onboard.SpecRepository, onboard.SpecImageName, treePaths)
			if len(filteredTags) > 0 && len(existingTags) > 0 {
				template := existingTags[len(existingTags)-1].Name
				onboard.Tag = semver.TagNames(filteredTags)
				*onboardImages = append(*onboardImages, onboard)
				*isFirstOnboard = append(*isFirstOnboard, false)
				*templateTags = append(*templateTags, template)
			}
		} else if len(filteredTags) > 0 {
			// First-time onboard: keep only tags without existing specs
			onboard.Tag = semver.TagNames(filteredTags)
			*onboardImages = append(*onboardImages, onboard)
			*isFirstOnboard = append(*isFirstOnboard, true)
			*templateTags = append(*templateTags, "")
		}

		log.Printf("Onboard Data: %v\n", onboard)
	}
	return nil
}

// ─── Chunk 2 · TREE PARSING ─────────────────────────────────────────────────

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



