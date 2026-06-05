// ═══════════════════════════════════════════════════════════════════════════════
// Onboard —
//
//   Reads onboard.yml files from the remote spec repo and produces the
//   WorkGroups that drive the rest of the pipeline. specapi.SpecRepoFetchTree
//   populates pathcache.Cache as a side effect; specapi.SpecRepoFetchOnboard
//   decodes each file via workplan.Decode into decoded WorkGroups (group-level
//   metadata + Components, no Tag/PRID/Components yet); partnerrepo.ResolveTagCache
//   then fans each decoded group across its matched tags into one runtime
//   WorkGroup per tag — each with its own PRID, copied metadata, and one
//   *WorkComponent per component (Naming fully constructed).
//
//   Functions are ordered by call sequence:
//     FetchComponents()
//       → SpecRepoFetchTree()       (specapi, populates pathcache)
//       → buildGroups()
//           → filterOnboardFile()
//           → splitOnboardPath()
//           → SpecRepoFetchOnboard()         (specapi)
//           → expandGroups()
//               → partnerrepo.ResolveTagCache()  — per decoded WorkGroup
// ═══════════════════════════════════════════════════════════════════════════════

package specrepo

import (
	"fmt"
	"log"
	"strings"

	"dalec-mapping/domain/pathcache"
	"dalec-mapping/domain/tagcache"
	"dalec-mapping/domain/workplan"
	"dalec-mapping/workflow/infrastructure/specapi"
	"dalec-mapping/workflow/services/partnerrepo"
)

// FetchComponents reads partner-level onboard.yml files from the spec repo
// and produces the runtime WorkGroups that drive the rest of the pipeline.
// SpecRepoFetchTree populates pathcache.Cache as a side effect, so later
// phases can answer "does this remote path exist?" via pathcache.Has. Each
// onboard top-level key produces one decoded WorkGroup, which ResolveTagCache
// fans across its matched tags into one runtime WorkGroup per tag (with its
// own PRID and a fully-constructed Naming on each per-component WorkComponent).
func FetchComponents(inputPath string) ([]workplan.WorkGroup, error) {
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
		totalItems += len(group.Components)
	}
	log.Printf("Output: %d work components across %d group(s), %d existing paths indexed\n", totalItems, len(groups), len(pathcache.Cache))
	return groups, nil
}

// buildGroups walks the spec repo tree once. For each onboard.yml under
// inputPath it fetches the decoded groups (via SpecRepoFetchOnboard) and
// hands them to expandGroups for per-tag fan-out.
func buildGroups(specRepoEntries []interface{}, inputPath string) []workplan.WorkGroup {
	var groups []workplan.WorkGroup

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

// expandGroups dispatches each decoded WorkGroup to partnerrepo.ResolveTagCache
// for per-tag fan-out. The decoded group carries group-level metadata and
// the static Components slice; ResolveTagCache emits one runtime WorkGroup
// per matched tag with PRID, copied metadata, and per-component WorkItems
// whose Naming is fully constructed.
func expandGroups(onboardGroups []workplan.WorkGroup, partnerOnboardDir string) []workplan.WorkGroup {
	var runtimeGroups []workplan.WorkGroup
	for groupIndex := range onboardGroups {
		decoded := &onboardGroups[groupIndex]
		runtimeGroups = append(runtimeGroups, partnerrepo.ResolveTagCache(decoded, partnerOnboardDir)...)
	}
	return runtimeGroups
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
// ResolveTagCache appends each component's Name to produce the per-component
// OnboardDir.
func splitOnboardPath(onboardPath string) (string, error) {
	segments := strings.Split(onboardPath, "/")
	segmentCount := len(segments)
	if segmentCount < 3 {
		return "", fmt.Errorf("unexpected file path format: %s (expected <prefix>/<partner>/onboard.yml)", onboardPath)
	}
	return strings.Join(segments[:segmentCount-1], "/"), nil
}
