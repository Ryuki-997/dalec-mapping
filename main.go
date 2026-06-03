package main

import (
	"log"

	"dalec-mapping/domain/buildresult"
	"dalec-mapping/domain/naming"
	"dalec-mapping/workflow/foundations/logging"
	"dalec-mapping/workflow/infrastructure/patching"
	"dalec-mapping/workflow/orchestration"
)

// ═══════════════════════════════════════════════════════════════════════════════
// Main — Dalec Spec Pipeline (wiring only)
//
//   Three explicit phases, each with a typed input and output:
//
//     Phase 1  Resolve   ()                → workplan.WorkPlan
//     Phase 2  Generate  workplan.WorkPlan          → []buildresult.BuildResult
//     Phase 3  Publish   []buildresult.BuildResult     → batches → []PublishOutcome
//
//   Per-spec side effects (golden diff, local cache write, action log) live
//   between phases 2 and 3 as pure observers over the buildresult.BuildResult slice.
// ═══════════════════════════════════════════════════════════════════════════════

func main() {
	inputPath, patchMode, noPublish := orchestration.ParseFlags()
	orchestration.LoadEnv()

	if patchMode {
		runPatchWorkflow()
		return
	}

	// ── Phase 1: Resolve onboard files and tag patterns into flat work items ──
	plan := orchestration.Resolve(inputPath)

	// ── Phase 2: Generate spec per item (no PR/grouping logic) ──
	results := orchestration.Generate(plan)

	// ── Observers: local cache, golden diff, action log ──
	observeResults(results)

	if noPublish {
		log.Println("⚠️  -no-publish set: skipping Phase 3 (PR publishing)")
		return
	}

	// ── Phase 3: Group results, resolve naming, publish PRs ──
	batches := orchestration.GroupIntoBatches(results, naming.GeneratePRID)
	log.Printf("Total PR groups to submit: %d", len(batches))
	for _, batch := range batches {
		log.Printf("Group: %s, Components: %d", batch.Key, len(batch.Components))
	}

	outcomes := orchestration.Publish(batches, plan.ExistingPaths)
	orchestration.PrintPublishSummary(outcomes)
}

// observeResults runs side effects that happen once per buildresult.BuildResult but are
// not part of publishing: write the local generated copy, diff against the
// golden file, and accumulate the action log.
func observeResults(results []buildresult.BuildResult) {
	actionLog := make([]logging.ActionEntry, 0, len(results))
	for _, result := range results {
		item := result.Item
		actionLog = append(actionLog, logging.ActionEntry{
			Component: item.Naming.SpecImageName,
			Version:   item.Naming.VersionRevision,
			Action:    result.Outcome.String(),
		})

		switch result.Outcome {
		case buildresult.OutcomeSkipped:
			log.Printf("✅ PASS  %s @ %s [SKIPPED]", item.Naming.SpecImageName, item.Tag.Stripped)
			continue
		case buildresult.OutcomeFailed:
			continue
		}

		writeGenerated(result)
		diffWithGolden(result)
	}
	logging.PrintActionLog(actionLog)
}

// ─── Patching workflow (separate from the spec pipeline) ────────────────────

func runPatchWorkflow() {
	log.Println("Running patching workflow — scanning ACR images for vulnerabilities")

	scanResults, err := patching.FetchAndScanACRImages()
	if err != nil {
		log.Fatalf("❌ Patching workflow failed: %v", err)
	}

	if len(scanResults) == 0 {
		log.Println("  No ACR images found to scan")
		return
	}

	report, err := patching.BuildPatchReport(scanResults)
	if err != nil {
		log.Fatalf("❌ Failed to build patch report: %v", err)
	}

	if report.TotalReflective == 0 && report.TotalNonReflective == 0 {
		log.Println("  ✅ No vulnerabilities found across all images")
	}

	log.Println("Patching workflow complete")
}
