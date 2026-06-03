// ═══════════════════════════════════════════════════════════════════════════════
// Onboard —
//
//   Reads onboard.yml files from the remote spec repo. For each onboard file
//   found, the per-component walk + tag resolution is performed by
//   partnerrepo.ResolveTagCache so the caller gets a flat slice of WorkItems
//   ready for downstream phases.
//
//   Functions are ordered by call sequence:
//     FetchComponents()
//       → fetchSpecRepoTree()
//       → resolveOnboardFiles()
//           → filterOnboardFile()
//           → splitOnboardPath()
//           → fetchOnboardFile() (specapi.SpecRepoFetchOnboard)
//           → partnerrepo.ResolveTagCache()
// ═══════════════════════════════════════════════════════════════════════════════

package specrepo

import (
	"fmt"
	"log"
	"strings"

	"dalec-mapping/domain/naming"
	"dalec-mapping/domain/tagcache"
	"dalec-mapping/domain/workplan"
	"dalec-mapping/workflow/infrastructure/specapi"
	"dalec-mapping/workflow/services/partnerrepo"
)

// FetchComponents reads partner-level onboard.yml files from the spec repo,
// walks each file per-component (standalone and grouped), and expands every
// component into workplan.WorkItems via partnerrepo.ResolveTagCache. Returns
// the WorkItems and the set of existing file paths in the spec repo (used by
// phase 2 for revision calculation).
func FetchComponents(inputPath string) ([]workplan.WorkItem, map[string]bool, error) {
	log.Printf("Full onboard search path: %s\n", inputPath)

	tagcache.Init()

	specRepoEntries, existingPaths, err := specapi.SpecRepoFetchTree()
	if err != nil {
		return nil, nil, err
	}

	items, err := resolveOnboardFiles(specRepoEntries, inputPath, existingPaths)
	if err != nil {
		return nil, nil, err
	}

	log.Printf("Output: %d work items resolved, %d existing paths indexed\n", len(items), len(existingPaths))
	return items, existingPaths, nil
}

// resolveOnboardFiles iterates the spec repo tree entries. For each onboard.yml
// found under inputPath, it fetches the file and calls ResolveTagCache to
// produce WorkItems for every component.
func resolveOnboardFiles(specRepoEntries []interface{}, inputPath string, existingPaths map[string]bool) ([]workplan.WorkItem, error) {
	var items []workplan.WorkItem

	for _, entry := range specRepoEntries {
		onboardPath, ok := filterOnboardFile(entry, inputPath)
		if !ok {
			continue
		}
		log.Println()
		log.Printf("Processing onboard file: %s\n", onboardPath)

		baseItem, err := splitOnboardPath(onboardPath)
		if err != nil {
			continue
		}

		onboardFile, err := specapi.SpecRepoFetchOnboard(onboardPath)
		if err != nil {
			log.Printf("⚠️  %v\n", err)
			continue
		}

		fileItems, err := partnerrepo.ResolveTagCache(onboardFile, baseItem, existingPaths)
		if err != nil {
			log.Printf("⚠️  %v\n", err)
			continue
		}

		log.Println()
		items = append(items, fileItems...)
	}
	return items, nil
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

// splitOnboardPath extracts the onboard directory from an onboard.yml path
// like "<prefix>/<partner>/onboard.yml" and returns a workplan.WorkItem
// seeded with the path-derived Naming runtime field (OnboardDir).
// The atomic section (SpecRepository, SpecImageName) and the embedded
// OnboardingComponent (including Name + GroupName) are filled later by
// walkOnboardFile.
func splitOnboardPath(onboardPath string) (workplan.WorkItem, error) {
	segments := strings.Split(onboardPath, "/")
	segmentCount := len(segments)
	if segmentCount < 3 {
		return workplan.WorkItem{}, fmt.Errorf("unexpected file path format: %s (expected <prefix>/<partner>/onboard.yml)", onboardPath)
	}
	return workplan.WorkItem{
		Naming: naming.Naming{
			OnboardDir: strings.Join(segments[:segmentCount-1], "/"),
		},
	}, nil
}
