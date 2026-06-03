// ═══════════════════════════════════════════════════════════════════════════════
// Phase 1 — Resolve
//
//   Input:  inputPath (path or URL fragment to onboard files)
//   Output: workplan.WorkPlan{Items, ExistingPaths}
//
//   Responsibilities:
//     - Fetch onboard configs and expand tag patterns into WorkItems
//       (specrepo.FetchComponents → partnerrepo.ResolveTagCache per file)
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
func Resolve(inputPath string) workplan.WorkPlan {
	log.Println("═══ Phase 1: Resolve ═══")

	items, existingPaths := fetchWorkItems(inputPath)

	return workplan.WorkPlan{
		Items:         items,
		ExistingPaths: existingPaths,
	}
}

// fetchWorkItems runs specrepo.FetchComponents and logs the resulting
// (component, tag) queue. Fatals on error or empty results.
func fetchWorkItems(inputPath string) ([]workplan.WorkItem, map[string]bool) {
	log.Println("─── Fetch onboard files and resolve tag cache ───")
	log.Printf("Input path: %s", inputPath)

	items, existingPaths, err := specrepo.FetchComponents(inputPath)
	if err != nil {
		log.Fatalf("❌ Failed to fetch onboard data: %v", err)
	}

	if len(items) == 0 {
		log.Fatalf("❌ No actionable tags found for any component at path: %s", inputPath)
	}

	tagsByComponent := make(map[string][]string)
	for _, item := range items {
		name := item.Naming.SpecImageName
		tagsByComponent[name] = append(tagsByComponent[name], fmt.Sprintf("%s (R%d)", item.Tag.Stripped, item.Tag.Revision))
	}
	log.Printf("Tag cache (%d tags across %d components):", len(items), len(tagsByComponent))
	for component, tagList := range tagsByComponent {
		log.Printf("  %s:", component)
		for _, tag := range tagList {
			log.Printf("    %s", tag)
		}
	}
	log.Println()

	return items, existingPaths
}
