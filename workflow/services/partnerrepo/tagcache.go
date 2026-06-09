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
//       fully populated (Naming.Construct already called) with a ParentGroup
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
// component's Naming is fully constructed and ParentGroup back-pointer set
// before return. Returns nil when the group has no repository, no tag patterns,
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
// resolved from the onboard.yml entry. Include/exclude patterns are intentionally
// omitted — excluded tags are already reported inline via `Excluded tag "x"`.
func logGroupOnboardData(group *workplan.WorkGroup) {
	log.Printf("  Onboard: %s (repo=%s, %d component(s))",
		group.GroupName, group.Repository, len(group.Components))
}

// fetchComponentTags returns the tag→commit map for a repository URL.
// Results are cached in tagcache.Cache; repeated calls for the same
// repo return the cached value.
func fetchComponentTags(repoURL string) map[string]string {
	if repoTags, cached := tagcache.Cache[repoURL]; cached {
		return repoTags
	}

	log.Printf("  Fetching tags for %s...", repoURL)
	repoTags, err := semver.FetchRepoTags(repoURL)
	if err != nil {
		log.Printf("  ⚠️  Failed to fetch tags for %s: %v", repoURL, err)
		tagcache.Cache[repoURL] = make(map[string]string)
		return nil
	}

	log.Printf("  ✅ Fetched %d tags for %s", len(repoTags), repoURL)
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
// metadata, and cloned per-component *WorkComponents whose ParentGroup
// back-pointer is set. Naming carries the atomic identity only; Phase 2
// resolveAction will call Naming.Construct once the action branch is decided
// and the component's Revision is finalized. Returns ok=false when the group
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
	return runtimeGroup, true
}

// buildComponentsForTag emits one runtime *WorkComponent per skeleton declared
// under the onboard group, all sharing the supplied tag and the runtime
// WorkGroup back-pointer. Per-component baseNaming is derived from
// partnerOnboardDir + "/" + skeleton.Name; Revision is left at zero (Phase 2's
// resolveAction sets it once the action branch is decided). Components whose
// derived OnboardDir lacks a "specs/" anchor are skipped with a warning.
func buildComponentsForTag(group *workplan.WorkGroup, runtimeGroup *workplan.WorkGroup, partnerOnboardDir string, tag tags.TagSet) []*workplan.WorkComponent {
	components := make([]*workplan.WorkComponent, 0, len(group.Components))
	for _, skeleton := range group.Components {
		baseNaming := naming.Naming{
			OnboardDir: partnerOnboardDir + "/" + skeleton.Name,
		}
		baseNaming.DeriveAtomic()
		if baseNaming.SpecImageName == "" {
			log.Printf("⚠️  Skipping %s: no \"specs/\" anchor in onboard path %q", skeleton.Name, baseNaming.OnboardDir)
			continue
		}

		component := &workplan.WorkComponent{
			Name:          skeleton.Name,
			DockerfileDir: skeleton.DockerfileDir,
			MakefileDir:   skeleton.MakefileDir,
			ParentGroup:   runtimeGroup,
			Naming:        baseNaming,
			Tag:           tag,
		}
		components = append(components, component)
	}
	return components
}
