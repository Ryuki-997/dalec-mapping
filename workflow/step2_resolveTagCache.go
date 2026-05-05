// ═══════════════════════════════════════════════════════════════════════════════
// Step 2 — Resolve Tag Cache
//
//   Fetches repository tags for every component, matches them against the
//   component's tag patterns, and expands the component-level states into
//   (component, tag) pairs. Also populates the global pipeline.TagCache.
//
//   Functions are ordered by call sequence:
//     ResolveTagCache()
//       → fetchComponentTags()
//       → matchTagPatterns()
// ═══════════════════════════════════════════════════════════════════════════════

package workflow

import (
	"log"

	"dalec-mapping/domain/onboarding"
	"dalec-mapping/infrastructure/semver"
	"dalec-mapping/pipeline"
)

// ResolveTagCache takes component-level states (one per component) and expands
// each into (component, tag) states by matching tag patterns against the
// source repository. Populates pipeline.TagCache as a side effect.
func ResolveTagCache(componentStates []pipeline.State, existingPaths map[string]bool) ([]pipeline.State, error) {
	pipeline.InitTagCache()

	var states []pipeline.State
	for _, state := range componentStates {
		component := state.Onboard

		repoTags := fetchComponentTags(component.Repository)
		if len(repoTags) == 0 {
			continue
		}

		states = append(states, matchTagPatterns(component, repoTags, existingPaths)...)
	}

	log.Printf("Step 2 output: %d (component, tag) states resolved, %d repos cached\n", len(states), len(pipeline.TagCache))
	log.Println()
	return states, nil
}

// fetchComponentTags returns the tag→commit map for a repository URL.
// Results are cached in pipeline.TagCache; repeated calls for the same
// repo return the cached value.
func fetchComponentTags(repoURL string) map[string]string {
	if repoTags, cached := pipeline.TagCache[repoURL]; cached {
		return repoTags
	}

	log.Printf("Fetching tags for %s...\n", repoURL)
	repoTags, err := semver.FetchRepoTags(repoURL)
	if err != nil {
		log.Printf("⚠️  Failed to fetch tags for %s: %v\n", repoURL, err)
		pipeline.TagCache[repoURL] = make(map[string]string)
		return nil
	}

	log.Printf("✅ Fetched %d tags for %s\n", len(repoTags), repoURL)
	pipeline.TagCache[repoURL] = repoTags
	return repoTags
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
