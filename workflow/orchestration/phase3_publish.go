// ═══════════════════════════════════════════════════════════════════════════════
// Phase 3 — Publish
//
//   Input:  []workplan.WorkGroup (produced by Phase 1, decorated by
//           Phase 2 — every component's Result is now populated).
//   Output: []PublishOutcome
//
//   Iterates the groups in order. specrepo.CreatePR filters publishable
//   components per group and opens one PR; groups with no publishable components are
//   skipped; per-group errors are logged but do not stop later groups. PRID +
//   BranchName + PRTitle are already baked into every component's Naming during
//   Phase 1.
// ═══════════════════════════════════════════════════════════════════════════════

package orchestration

import (
	"log"

	"dalec-mapping/domain/workplan"
	"dalec-mapping/workflow/services/specrepo"
)

// PublishOutcome records the result of opening one PR.
type PublishOutcome struct {
	GroupName string
	URL       string
	Created   bool // false if an existing PR was reused
	SpecPaths []string
	Err       error
}

// Publish walks every WorkGroup and opens one PR per group that has at
// least one publishable component. Phase 3 is read-only over the groups; it does
// not mutate WorkComponents. Filtering of publishable components happens inside
// specrepo.CreatePR.
func Publish(groups []workplan.WorkGroup) []PublishOutcome {
	log.Println("═══ Phase 3: Publish ═══")

	outcomes := make([]PublishOutcome, 0, len(groups))
	for _, group := range groups {
		outcome, ok := publishGroup(group)
		if !ok {
			continue
		}
		outcomes = append(outcomes, outcome)
	}
	log.Printf("  Groups to submit: %d", len(outcomes))
	return outcomes
}

// publishGroup invokes specrepo.CreatePR for one group. Returns (outcome, true)
// when the group produced a PR (created or reused) or hit a hard error, and
// (zero, false) when the group had no publishable components and was skipped.
func publishGroup(group workplan.WorkGroup) (PublishOutcome, bool) {
	url, created, specPaths, err := specrepo.CreatePR(group)
	if err != nil {
		log.Printf("  ❌ PR creation failed for %s: %v", group.GroupName, err)
		return PublishOutcome{GroupName: group.GroupName, Err: err}, true
	}
	if url == "" {
		return PublishOutcome{}, false
	}
	log.Printf("  Group: %s (%d components)", group.GroupName, len(specPaths))
	return PublishOutcome{
		GroupName: group.GroupName,
		URL:       url,
		Created:   created,
		SpecPaths: specPaths,
	}, true
}

// PrintPublishSummary renders the PR creation roll-up as a sub-section under
// the Finalization banner. No banner box; entries are indented to nest under
// the `PR Summary:` header.
func PrintPublishSummary(outcomes []PublishOutcome) {
	createdCount := 0
	skippedCount := 0
	for _, outcome := range outcomes {
		if outcome.Err != nil {
			continue
		}
		if outcome.Created {
			createdCount++
			continue
		}
		skippedCount++
	}

	log.Printf("  PR Summary: %d created, %d already-open", createdCount, skippedCount)
	prIndex := 0
	for _, outcome := range outcomes {
		if outcome.Err != nil {
			continue
		}
		prIndex++
		label := "created"
		if !outcome.Created {
			label = "existing"
		}
		log.Printf("    PR #%d [%s]: %s", prIndex, label, outcome.URL)
		for _, file := range outcome.SpecPaths {
			log.Printf("      - %s", file)
		}
	}
}
