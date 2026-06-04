// ═══════════════════════════════════════════════════════════════════════════════
// Onboard —
//
//   Reads onboard.yml files from the remote spec repo and produces the
//   WorkItemGroups that drive the rest of the pipeline. specapi.SpecRepoFetchTree
//   populates pathcache.Cache as a side effect; specapi.SpecRepoFetchOnboard
//   decodes each file via workplan.Decode into WorkItemGroups (one *WorkItem
//   per declared component with only .Component populated);
//   partnerrepo.ResolveTagCache expands each item into per-tag *WorkItems.
//   This layer is the single centralization point: it walks every group,
//   mints the group's PRID, and calls Naming.Construct on each expanded item.
//
//   Functions are ordered by call sequence:
//     FetchComponents()
//       → SpecRepoFetchTree()       (specapi, populates pathcache)
//       → buildGroups()
//           → filterOnboardFile()
//           → splitOnboardPath()
//           → SpecRepoFetchOnboard()         (specapi)
//           → partnerrepo.ResolveTagCache()  — per *WorkItem
// ═══════════════════════════════════════════════════════════════════════════════

package specrepo

import (
	"fmt"
	"log"
	"strings"

	"dalec-mapping/domain/naming"
	"dalec-mapping/domain/pathcache"
	"dalec-mapping/domain/tagcache"
	"dalec-mapping/domain/workplan"
	"dalec-mapping/workflow/infrastructure/specapi"
	"dalec-mapping/workflow/services/partnerrepo"
)

// FetchComponents reads partner-level onboard.yml files from the spec repo
// and produces the WorkItemGroups that drive the rest of the pipeline.
// SpecRepoFetchTree populates pathcache.Cache as a side effect, so later
// phases can answer "does this remote path exist?" via pathcache.Has.
// Each onboard file's top-level YAML keys become one WorkItemGroup; PRID is
// minted once per group and every workitem's Generated Naming section is
// filled by Naming.Construct(tag, group.GroupName, group.PRID) before being
// appended to its group's Items.
func FetchComponents(inputPath string) ([]workplan.WorkItemGroup, error) {
	log.Printf("Full onboard search path: %s\n", inputPath)

	tagcache.Init()
	pathcache.Init()

	specRepoEntries, err := specapi.SpecRepoFetchTree()
	if err != nil {
		return nil, err
	}

	groups := buildGroups(specRepoEntries, inputPath)

	totalItems := 0
	for _, group := range groups {
		totalItems += len(group.Items)
	}
	log.Printf("Output: %d work items across %d group(s), %d existing paths indexed\n", totalItems, len(groups), len(pathcache.Cache))
	return groups, nil
}

// buildGroups walks the spec repo tree once. For each onboard.yml under
// inputPath it fetches the groups (via SpecRepoFetchOnboard), expands each
// group's items by tag (via ResolveTagCache), mints the group's PRID once,
// and calls Naming.Construct on every expanded item. Empty groups (no
// actionable items) are dropped.
func buildGroups(specRepoEntries []interface{}, inputPath string) []workplan.WorkItemGroup {
	var groups []workplan.WorkItemGroup

	for _, entry := range specRepoEntries {
		onboardPath, ok := filterOnboardFile(entry, inputPath)
		if !ok {
			continue
		}
		log.Println()
		log.Printf("Processing onboard file: %s\n", onboardPath)

		partnerOnboardDir, err := splitOnboardPath(onboardPath)
		if err != nil {
			continue
		}

		onboardGroups, err := specapi.SpecRepoFetchOnboard(onboardPath)
		if err != nil {
			log.Printf("⚠️  %v\n", err)
			continue
		}

		groups = append(groups, expandGroups(onboardGroups, partnerOnboardDir)...)

		log.Println()
	}

	return groups
}

// expandGroups turns the groups parsed from one onboard.yml into the
// fully-centralized groups that go into the workplan. Each input group gets
// one minted PRID; every *WorkItem inside it is fanned out by tag and each
// expanded item has Naming.Construct called once.
func expandGroups(onboardGroups []workplan.WorkItemGroup, partnerOnboardDir string) []workplan.WorkItemGroup {
	groups := make([]workplan.WorkItemGroup, 0, len(onboardGroups))
	for _, onboardGroup := range onboardGroups {
		group := workplan.WorkItemGroup{
			GroupName: onboardGroup.GroupName,
			PRID:      naming.GeneratePRID(),
		}
		for _, item := range onboardGroup.Items {
			item.Naming.OnboardDir = fmt.Sprintf("%s/%s", partnerOnboardDir, item.Component.Name)
			expandedItems := partnerrepo.ResolveTagCache(item)
			for _, expanded := range expandedItems {
				expanded.Naming.Construct(expanded.Tag, group.GroupName, group.PRID)
			}
			group.Items = append(group.Items, expandedItems...)
		}
		if len(group.Items) == 0 {
			continue
		}
		groups = append(groups, group)
	}
	return groups
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

// splitOnboardPath extracts the partner onboard directory from an onboard.yml
// path like "<prefix>/<partner>/onboard.yml". The returned string is the
// directory containing the onboard file (e.g. "specs/containernetworking");
// expandGroups appends each component's Name to produce the per-component
// OnboardDir.
func splitOnboardPath(onboardPath string) (string, error) {
	segments := strings.Split(onboardPath, "/")
	segmentCount := len(segments)
	if segmentCount < 3 {
		return "", fmt.Errorf("unexpected file path format: %s (expected <prefix>/<partner>/onboard.yml)", onboardPath)
	}
	return strings.Join(segments[:segmentCount-1], "/"), nil
}
