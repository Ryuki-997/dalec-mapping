// ══════════════════════════════════════════════════════════════════════════════════════════
// Tags — Resolve Tag Cache (per-group, fans into per-tag runtime groups)
//
//   Receives a single decoded *WorkGroup (group-level metadata + skeleton
//   Components, no Tag/PRID/Naming yet) and the partner onboard directory, then
//   expands it into one runtime *workplan.WorkGroup per actionable tag.
//   Each runtime group carries:
//     • A freshly minted PRID (one PR per tag).
//     • The resolved Tag.
//     • Copies of the group's metadata (Repository/TagPatterns/Targets/
//       License/Reviewers).
//     • One *WorkComponent per skeleton, cloned from the decoded group and
//       fully populated (Naming.Construct already called) with a Group
//       back-pointer to the runtime WorkGroup.
//
//   Functions are ordered by call sequence:
//     ResolveTagCache()
//       → logGroupOnboardData()
//       → fetchComponentTags()
//       → resolveMatchedTagNames()
//       → buildRuntimeGroup()          — per matched tag
//           → buildComponentsForTag()       — per skeleton within the tag
// ═══════════════════════════════════════════════════════════════════════════

package partnerrepo

import (
	"log"

	"dalec-mapping/domain/naming"
	"dalec-mapping/domain/tagcache"
	"dalec-mapping/domain/tags"
	"dalec-mapping/domain/workplan"
	"dalec-mapping/workflow/infrastructure/semver"
)

// ResolveTagCache fans a decoded onboard WorkGroup across its matched tags
// into one runtime WorkGroup per tag. Each runtime group has its own PRID
// and one *WorkComponent per skeleton declared under the onboard group; every
// component's Naming is fully constructed and Group back-pointer set before
// return. Returns nil when the group has no repository, no tag patterns,
// or no actionable tags.
func ResolveTagCache(group *workplan.WorkGroup, partnerOnboardDir string) []workplan.WorkGroup {
	logGroupOnboardData(group)

	repoTags := fetchComponentTags(group.Repository)
	if len(repoTags) == 0 {
		return nil
	}

	matchedTagNames := resolveMatchedTagNames(group, repoTags)
	if len(matchedTagNames) == 0 {
		return nil
	}

	runtimeGroups := make([]workplan.WorkGroup, 0, len(matchedTagNames))
	for _, tagName := range matchedTagNames {
		if _, exists := repoTags[tagName]; !exists {
			log.Printf("⚠️  Resolved tag %q not found in tags cache, skipping\n", tagName)
			continue
		}
		runtimeGroup, ok := buildRuntimeGroup(group, partnerOnboardDir, tagName)
		if !ok {
			continue
		}
		runtimeGroups = append(runtimeGroups, runtimeGroup)
	}
	return runtimeGroups
}

// logGroupOnboardData logs a single per-group line describing the inputs
// resolved from the onboard.yml entry. Component-level identity is logged
// later per component during fan-out.
func logGroupOnboardData(group *workplan.WorkGroup) {
	log.Printf("Onboard Data: %s repo=%s include=%v exclude=%v components=%d\n",
		group.GroupName, group.Repository,
		group.TagPatterns.Include, group.TagPatterns.Exclude,
		len(group.Components))
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

// resolveMatchedTagNames applies the group's tag patterns against the
// pre-fetched repo tags and returns the actionable tag name set. Logs and
// returns nil when the group has no patterns or nothing matches.
func resolveMatchedTagNames(group *workplan.WorkGroup, repoTags map[string]string) []string {
	if !group.TagPatterns.HasPatterns() {
		log.Printf("⚠️  No tag patterns defined for %s, skipping\n", group.GroupName)
		return nil
	}

	matched := semver.ResolveTagPatterns(repoTags, group.TagPatterns.Include, group.TagPatterns.Exclude)
	if len(matched) == 0 {
		log.Printf("Skipping %s: no tags matched include=%v exclude=%v\n",
			group.GroupName, group.TagPatterns.Include, group.TagPatterns.Exclude)
		return nil
	}
	return matched
}

// buildRuntimeGroup materializes one per-tag WorkGroup with PRID, Tag, copied
// metadata, and cloned per-component *WorkItems whose Naming is fully
// constructed and Group back-pointer set. Returns ok=false when the group
// ends up with no components.
func buildRuntimeGroup(group *workplan.WorkGroup, partnerOnboardDir, tagName string) (workplan.WorkGroup, bool) {
	tag := tags.TagSet{Full: tagName}
	tag.Resolve()

	runtimeGroup := workplan.WorkGroup{
		GroupName:   group.GroupName,
		PRID:        naming.GeneratePRID(),
		Repository:  group.Repository,
		TagPatterns: group.TagPatterns,
		Targets:     group.Targets,
		License:     group.License,
		Reviewers:   group.Reviewers,
	}

	components := buildComponentsForTag(group, &runtimeGroup, partnerOnboardDir, tag)
	if len(components) == 0 {
		return workplan.WorkGroup{}, false
	}
	runtimeGroup.Components = components

	for _, component := range components {
		component.Naming.Construct(component.Tag, component.Revision, runtimeGroup.GroupName, runtimeGroup.PRID)
	}
	return runtimeGroup, true
}

// buildComponentsForTag emits one runtime *WorkComponent per skeleton declared under
// the onboard group, all sharing the supplied tag and the runtime
// WorkGroup back-pointer. Per-component baseNaming is derived from
// partnerOnboardDir + "/" + skeleton.Name; the next revision is looked up
// in pathcache via semver.FindLatestRevision (per-component history).
func buildComponentsForTag(group *workplan.WorkGroup, runtimeGroup *workplan.WorkGroup, partnerOnboardDir string, tag tags.TagSet) []*workplan.WorkComponent {
	components := make([]*workplan.WorkComponent, 0, len(group.Components))
	for _, skeleton := range group.Components {
		baseNaming := naming.Naming{
			OnboardDir: partnerOnboardDir + "/" + skeleton.Name,
		}
		baseNaming.DeriveAtomic()

		latestRevision, found := semver.FindLatestRevision(baseNaming, tag.Version)
		nextRevision := 1
		if found {
			nextRevision = latestRevision + 1
		}

		component := &workplan.WorkComponent{
			Name:          skeleton.Name,
			DockerfileDir: skeleton.DockerfileDir,
			MakefileDir:   skeleton.MakefileDir,
			Group:         runtimeGroup,
			Naming:        baseNaming,
			Tag:           tag,
			Revision:      nextRevision,
		}
		components = append(components, component)
		log.Printf("Queued: %s @ %s\n", baseNaming.SpecImageName, tag.Stripped)
	}
	return components
}
