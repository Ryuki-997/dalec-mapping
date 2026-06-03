// ═══════════════════════════════════════════════════════════════════════════════
// Tags — Resolve Tag Cache
//
//   Walks one parsed onboard.yml file, fetches repository tags for every
//   component (standalone and grouped), matches them against the component's
//   tag patterns, and expands the component-level WorkItems into
//   (component, tag) WorkItems. Also populates the global tagcache.Cache.
//
//   The unit passed through every step is workplan.WorkItem; each step
//   enriches the same item rather than introducing parallel types.
//
//   Functions are ordered by call sequence:
//     ResolveTagCache()
//       → walkOnboardFile()
//       → fetchComponentTags()
//       → matchTagPatterns()
// ═══════════════════════════════════════════════════════════════════════════════

package partnerrepo

import (
	"fmt"
	"log"

	"dalec-mapping/domain/onboarding"
	"dalec-mapping/domain/tagcache"
	"dalec-mapping/domain/tags"
	"dalec-mapping/domain/workplan"
	"dalec-mapping/workflow/infrastructure/semver"
)

// ResolveTagCache walks one OnboardFile, expands it into per-component
// WorkItems, and then into (component, tag) WorkItems by matching tag
// patterns against the source repository. Populates tagcache.Cache as a
// side effect.
//
//   - file: parsed onboard.yml (targets already validated during unmarshal)
//   - base: workplan.WorkItem seeded with path-derived runtime naming fields
//     (Naming.OnboardDir, Naming.SpecRepository) by the caller.
func ResolveTagCache(file onboarding.OnboardFile, base workplan.WorkItem, existingPaths map[string]bool) ([]workplan.WorkItem, error) {
	componentItems := walkOnboardFile(file, base)

	var items []workplan.WorkItem
	for _, componentItem := range componentItems {
		logOnboardData(componentItem)

		repoTags := fetchComponentTags(componentItem.Naming.Repository)
		if len(repoTags) == 0 {
			continue
		}

		items = append(items, matchTagPatterns(componentItem, repoTags, existingPaths)...)
	}

	log.Printf("Output: %d (component, tag) items resolved, %d repos cached\n", len(items), len(tagcache.Cache))
	log.Println()
	return items, nil
}

// walkOnboardFile flattens an OnboardFile into a slice of WorkItems with the
// Naming runtime section populated. The base WorkItem carries the
// path-derived runtime fields (Naming.OnboardDir, Naming.SpecRepository);
// per-component fields (SpecImageName, GroupName) and the embedded
// ComponentConfig are filled here. The single-standalone rule (when an
// onboard file has exactly one standalone component whose name matches the
// partner folder, omit the partner prefix from paths) is applied here.
func walkOnboardFile(file onboarding.OnboardFile, base workplan.WorkItem) []workplan.WorkItem {
	var items []workplan.WorkItem

	onboardDir := base.Naming.OnboardDir
	specRepository := base.Naming.SpecRepository

	singleStandalone := len(file.Standalone) == 1 && len(file.Groups) == 0

	for name, cfg := range file.Standalone {
		item := base
		item.Naming.ComponentConfig = cfg
		item.Naming.SpecImageName = name
		if singleStandalone && name == specRepository {
			item.Naming.SpecRepository = ""
			item.Naming.OnboardDir = onboardDir
		} else {
			item.Naming.SpecRepository = specRepository
			item.Naming.OnboardDir = fmt.Sprintf("%s/%s", onboardDir, name)
		}
		items = append(items, item)
	}

	for groupName, group := range file.Groups {
		for name, cfg := range group {
			item := base
			item.Naming.ComponentConfig = cfg
			item.Naming.SpecImageName = name
			item.Naming.SpecRepository = specRepository
			item.Naming.OnboardDir = fmt.Sprintf("%s/%s", onboardDir, name)
			item.Naming.GroupName = groupName
			items = append(items, item)
		}
	}

	return items
}

// logOnboardData logs a single per-component line describing the inputs
// resolved from the onboard.yml entry.
func logOnboardData(item workplan.WorkItem) {
	naming := item.Naming
	if naming.SpecRepository != "" {
		log.Printf("Onboard Data: %s/%s repo=%s include=%v exclude=%v\n",
			naming.SpecRepository, naming.SpecImageName, naming.Repository,
			naming.TagPatterns.Include, naming.TagPatterns.Exclude)
		return
	}
	log.Printf("Onboard Data: %s repo=%s include=%v exclude=%v\n",
		naming.SpecImageName, naming.Repository,
		naming.TagPatterns.Include, naming.TagPatterns.Exclude)
}

// fetchComponentTags returns the tag→commit map for a repository URL.
// Results are cached in tagcache.Cache; repeated calls for the same
// repo return the cached value.
func fetchComponentTags(repoURL string) map[string]string {
	if repoTags, cached := tagcache.Cache[repoURL]; cached {
		return repoTags
	}

	log.Printf("Fetching tags for %s...\n", repoURL)
	repoTags, err := semver.FetchRepoTags(repoURL)
	if err != nil {
		log.Printf("⚠️  Failed to fetch tags for %s: %v\n", repoURL, err)
		tagcache.Cache[repoURL] = make(map[string]string)
		return nil
	}

	log.Printf("✅ Fetched %d tags for %s\n", len(repoTags), repoURL)
	tagcache.Cache[repoURL] = repoTags
	return repoTags
}

// matchTagPatterns applies the component's tag patterns against the pre-fetched
// repo tags and produces one workplan.WorkItem per actionable match. The
// component WorkItem (with Naming runtime section populated) is cloned per
// tag, and the generated section of its Naming is filled via Construct.
func matchTagPatterns(componentItem workplan.WorkItem, repoTags map[string]string, existingPaths map[string]bool) []workplan.WorkItem {
	naming := componentItem.Naming
	if !naming.TagPatterns.HasPatterns() {
		log.Printf("⚠️  No tag patterns defined for %s, skipping\n", naming.SpecImageName)
		return nil
	}

	includePatterns := naming.TagPatterns.Include
	excludePatterns := naming.TagPatterns.Exclude

	resolvedTagNames := semver.ResolveTagPatterns(repoTags, includePatterns, excludePatterns)
	if len(resolvedTagNames) == 0 {
		log.Printf("Skipping %s: no tags matched include=%v exclude=%v\n", naming.SpecImageName, includePatterns, excludePatterns)
		return nil
	}

	actionableTags := semver.MatchTagSets(repoTags, resolvedTagNames, naming, existingPaths)
	if len(actionableTags) == 0 {
		log.Printf("Skipping %s: no actionable tags after revision check\n", naming.SpecImageName)
		return nil
	}

	var items []workplan.WorkItem
	for _, actionableTag := range actionableTags {
		strippedTag := semver.ToTag(actionableTag.Name)
		tagSet := tags.NewSet(actionableTag.Name, "", strippedTag, actionableTag.NextRevision)

		item := componentItem
		item.Naming.Construct(tagSet)
		item.Tag = tagSet

		items = append(items, item)
		log.Printf("Queued: %s @ %s (R%d)\n", naming.SpecImageName, actionableTag.Name, actionableTag.NextRevision)
	}
	return items
}
