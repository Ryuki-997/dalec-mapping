// ══════════════════════════════════════════════════════════════════════════════════════════
// Tags — Resolve Tag Cache (per-component)
//
//   Receives a single *WorkItem (with Naming.OnboardDir + Component
//   already populated by the caller) and expands it into one *WorkItem per
//   actionable (component, tag) pair. Naming runtime+atomic sections are
//   filled here; the Generated section (BranchName/PRTitle/etc.) is left
//   for the caller to fill via Naming.Construct once the group's PRID is
//   known.
//
//   Functions are ordered by call sequence:
//     ResolveTagCache()
//       → logOnboardData()
//       → fetchComponentTags()
//       → matchTagPatterns()        — per actionable tag: new *WorkItem (Naming+Component+Tag)
// ═══════════════════════════════════════════════════════════════════════════════

package partnerrepo

import (
	"log"

	"dalec-mapping/domain/tagcache"
	"dalec-mapping/domain/tags"
	"dalec-mapping/domain/workplan"
	"dalec-mapping/workflow/infrastructure/semver"
)

// ResolveTagCache expands one *WorkItem into one *WorkItem per
// actionable (component, tag) pair.
//
// The input item must already carry:
//   - Naming.OnboardDir set to the component's directory
//     (e.g. "specs/azure-cni/azure-cni" for grouped components, or just
//     "specs/aks-node-controller" for standalone).
//   - Component populated from the onboard.yml decode.
//
// The atomic Naming section (SpecRepository, SpecImageName) is derived
// here; Tag is set per output item; the Generated section is left
// zero-valued. Returns nil when no actionable tags resolve for this
// component.
func ResolveTagCache(item *workplan.WorkItem) []*workplan.WorkItem {
	item.Naming.DeriveAtomic()

	logOnboardData(item)

	repoTags := fetchComponentTags(item.Component.Repository)
	if len(repoTags) == 0 {
		return nil
	}

	return matchTagPatterns(item, repoTags)
}

// logOnboardData logs a single per-component line describing the inputs
// resolved from the onboard.yml entry.
func logOnboardData(item *workplan.WorkItem) {
	n := item.Naming
	cfg := item.Component
	if n.SpecRepository != "" && n.SpecRepository != n.SpecImageName {
		log.Printf("Onboard Data: %s/%s repo=%s include=%v exclude=%v\n",
			n.SpecRepository, n.SpecImageName, cfg.Repository,
			cfg.TagPatterns.Include, cfg.TagPatterns.Exclude)
		return
	}
	log.Printf("Onboard Data: %s repo=%s include=%v exclude=%v\n",
		n.SpecImageName, cfg.Repository,
		cfg.TagPatterns.Include, cfg.TagPatterns.Exclude)
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

// matchTagPatterns applies the component's tag patterns against the
// pre-fetched repo tags and produces one *workplan.WorkItem per actionable
// match. Each result is a fresh allocation carrying the input item's Naming
// (runtime+atomic) and Component, plus the resolved Tag. The Generated
// naming fields are filled later by the caller once the group's PRID is
// known. Reads pathcache.Cache via semver.MatchTagSets for revision lookups.
func matchTagPatterns(item *workplan.WorkItem, repoTags map[string]string) []*workplan.WorkItem {
	cfg := item.Component
	if !cfg.TagPatterns.HasPatterns() {
		log.Printf("⚠️  No tag patterns defined for %s, skipping\n", item.Naming.SpecImageName)
		return nil
	}

	resolvedTagNames := semver.ResolveTagPatterns(repoTags, cfg.TagPatterns.Include, cfg.TagPatterns.Exclude)
	if len(resolvedTagNames) == 0 {
		log.Printf("Skipping %s: no tags matched include=%v exclude=%v\n", item.Naming.SpecImageName, cfg.TagPatterns.Include, cfg.TagPatterns.Exclude)
		return nil
	}

	actionableTags := semver.MatchTagSets(repoTags, resolvedTagNames, item.Naming)
	if len(actionableTags) == 0 {
		log.Printf("Skipping %s: no actionable tags after revision check\n", item.Naming.SpecImageName)
		return nil
	}

	var items []*workplan.WorkItem
	for _, actionableTag := range actionableTags {
		strippedTag := semver.ToTag(actionableTag.Name)
		tagSet := tags.NewSet(actionableTag.Name, "", strippedTag, actionableTag.NextRevision)

		expanded := &workplan.WorkItem{
			Naming:    item.Naming,
			Component: item.Component,
			Tag:       tagSet,
		}
		items = append(items, expanded)
		log.Printf("Queued: %s @ %s\n", expanded.Naming.SpecImageName, expanded.Tag.Stripped)
	}
	return items
}
