// ═══════════════════════════════════════════════════════════════════════════════
// Phase 1 — Resolve
//
//   Input:  inputPath (path or URL fragment to onboard files)
//   Output: []workplan.WorkItemGroup
//
//   Phase 1 is a thin wrapper around specrepo.FetchComponents. The remote
//   spec-repo path index is written to pathcache.Cache as a side effect of
//   the underlying SpecRepoFetchTree call. All grouping and PRID minting
//   happens inside specrepo.buildGroups: one WorkItemGroup per onboard
//   group key, PRID generated at group creation, each *WorkItem centralized
//   via a single Naming.Construct call so every Generated field — including
//   BranchName/PRTitle — is final at the end of Phase 1.
//
//   Downstream phases never look at the onboard file structure again.
//
//   Fatal on any error (preserves prior behavior).
// ═══════════════════════════════════════════════════════════════════════════════

package orchestration

import (
	"fmt"
	"log"

	"dalec-mapping/domain/workplan"
	"dalec-mapping/workflow/services/specrepo"
)

// Resolve runs Phase 1 of the pipeline.
func Resolve(inputPath string) []workplan.WorkItemGroup {
	log.Println("═══ Phase 1: Resolve ═══")
	log.Println("─── Fetch onboard files and resolve tag cache ───")
	log.Printf("Input path: %s", inputPath)

	groups, err := specrepo.FetchComponents(inputPath)
	if err != nil {
		log.Fatalf("❌ Failed to fetch onboard data: %v", err)
	}

	if len(groups) == 0 {
		log.Fatalf("❌ No actionable tags found for any component at path: %s", inputPath)
	}

	logTagCache(groups)

	return groups
}

// logTagCache prints the "Tag cache (N tags across M components)" summary
// by walking the groups produced by Phase 1.
func logTagCache(groups []workplan.WorkItemGroup) {
	tagsByComponent := make(map[string][]string)
	totalItems := 0
	for _, group := range groups {
		for _, item := range group.Items {
			tagsByComponent[item.Naming.SpecImageName] = append(
				tagsByComponent[item.Naming.SpecImageName],
				fmt.Sprintf("%s (R%d)", item.Tag.Stripped, item.Tag.Revision),
			)
			totalItems++
		}
	}

	log.Printf("Tag cache (%d tags across %d components):", totalItems, len(tagsByComponent))
	for component, tagList := range tagsByComponent {
		log.Printf("  %s:", component)
		for _, tag := range tagList {
			log.Printf("    %s", tag)
		}
	}
	log.Println()
}
