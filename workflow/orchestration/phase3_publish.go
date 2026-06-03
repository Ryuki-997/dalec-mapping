package orchestration

import (
	"log"

	"dalec-mapping/domain/buildresult"
	"dalec-mapping/domain/prbatch"
	"dalec-mapping/workflow/services/specrepo"
)

// ═══════════════════════════════════════════════════════════════════════════════
// Phase 3 — Publish
//
//   Input:  []buildresult.BuildResult (from Phase 2)
//   Output: []PublishOutcome
//
//   The phase is split into two small, separately-testable functions so the
//   group-then-publish flow is explicit:
//
//     1. GroupIntoBatches — pure; groups publishable results by (group, tag),
//                           assigns each batch a PRID, and fills per-component
//                           Naming (BranchName/PRTitle) in the same pass
//     2. Publish          — impure; commits files and opens pull requests
//
//   Failed/skipped results are dropped from batches but logged. The grouping
//   step is the only place that knows how multiple components collapse into
//   one PR.
// ═══════════════════════════════════════════════════════════════════════════════

// PRIDGenerator returns a unique PR identifier each time it is called.
// Injectable so grouping stays deterministic in tests.
type PRIDGenerator func() string

// PublishOutcome records the result of opening one PR.
type PublishOutcome struct {
	Key       prbatch.PRGroupKey
	URL       string
	Created   bool // false if an existing PR was reused
	SpecPaths []string
	Err       error
}

// ─── 1. Group ───────────────────────────────────────────────────────────────────

// GroupIntoBatches groups publishable BuildResults into PR batches keyed by
// (group name, tag), assigns each batch a PRID, and fills per-component
// Naming (BranchName/PRTitle) using that PRID. Non-publishable results
// (skipped, failed) are dropped. Batches are returned in insertion order
// (first occurrence wins) so the output is deterministic given a deterministic
// PRID generator.
func GroupIntoBatches(results []buildresult.BuildResult, idGen PRIDGenerator) []prbatch.PRBatch {
	indexByKey := make(map[prbatch.PRGroupKey]int)
	batches := make([]prbatch.PRBatch, 0)

	for _, result := range results {
		if !result.IsPublishable() {
			continue
		}

		key := groupKeyOf(result)
		index, exists := indexByKey[key]
		if !exists {
			batches = append(batches, prbatch.PRBatch{
				Key:  key,
				PRID: idGen(),
			})
			index = len(batches) - 1
			indexByKey[key] = index
			log.Printf("Assigned PR ID %s to group %s", batches[index].PRID, key)
		}

		batches[index].Components = append(batches[index].Components, prbatch.BatchComponent{
			Result: result,
			Naming: result.Item.Naming.WithPRID(batches[index].PRID),
		})
	}

	return batches
}

func groupKeyOf(result buildresult.BuildResult) prbatch.PRGroupKey {
	return prbatch.PRGroupKey{
		GroupName: result.Item.Component.GroupName,
		Tag:       result.Item.Tag.Stripped,
	}
}

// ─── 2. Publish ──────────────────────────────────────────────────────────────────

// Publish opens one PR per batch and returns the outcomes (including
// failures) in batch order. Continues past per-batch errors.
// existingPaths is the spec-repo path index from Phase 1; it is used to skip
// BuildFiles snapshot files that already exist remotely.
func Publish(batches []prbatch.PRBatch, existingPaths map[string]bool) []PublishOutcome {
	log.Println("═══ Phase 3: Publish ═══")
	log.Println("─── Create pull requests ───")
	log.Printf("  Groups to submit: %d", len(batches))

	outcomes := make([]PublishOutcome, 0, len(batches))
	for _, batch := range batches {
		outcomes = append(outcomes, publishBatch(batch, existingPaths))
	}
	return outcomes
}

func publishBatch(batch prbatch.PRBatch, existingPaths map[string]bool) PublishOutcome {
	url, created, err := specrepo.CreatePR(batch, existingPaths)
	if err != nil {
		log.Printf("❌ PR creation failed for %s: %v", batch.Key, err)
		return PublishOutcome{Key: batch.Key, Err: err}
	}

	specPaths := make([]string, 0, len(batch.Components))
	for _, component := range batch.Components {
		specPaths = append(specPaths, component.Naming.SpecFilePath)
	}
	return PublishOutcome{
		Key:       batch.Key,
		URL:       url,
		Created:   created,
		SpecPaths: specPaths,
	}
}

// PrintPublishSummary prints the standard PR creation summary block.
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

	log.Printf("PR Summary (%d created, %d skipped — already open):", createdCount, skippedCount)
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
		log.Printf("  PR #%d [%s]: %s", prIndex, label, outcome.URL)
		for _, file := range outcome.SpecPaths {
			log.Printf("    - %s", file)
		}
	}
	log.Println()
}
