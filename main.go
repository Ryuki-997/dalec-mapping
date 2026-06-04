package main

import (
	"log"

	"dalec-mapping/domain/buildresult"
	"dalec-mapping/domain/workplan"
	"dalec-mapping/workflow/foundations/logging"
	"dalec-mapping/workflow/infrastructure/patching"
	"dalec-mapping/workflow/orchestration"
)

// ═══════════════════════════════════════════════════════════════════════════════
// Main — Dalec Spec Pipeline (wiring only)
//
//   Three explicit phases over a single []workplan.WorkItemGroup:
//
//     Phase 1  Resolve  ()                            → []workplan.WorkItemGroup
//     Phase 2  Generate []workplan.WorkItemGroup      (mutates each item.Result in place)
//     Phase 3  Publish  []workplan.WorkItemGroup      → []orchestration.PublishOutcome
//
//   Per-spec side effects (golden diff, local cache write, action log) live
//   between phases 2 and 3 as pure observers walking groups → item.Result.
// ═══════════════════════════════════════════════════════════════════════════════

func main() {
	inputPath, patchMode, noPublish := orchestration.ParseFlags()
	orchestration.LoadEnv()

	if patchMode {
		runPatchWorkflow()
		return
	}

	// ── Phase 1: Resolve onboard files and tag patterns into grouped work items ──
	groups := orchestration.Resolve(inputPath)

	// ── Phase 2: Generate spec per item (writes item.Result; no PR/grouping logic) ──
	orchestration.Generate(groups)

	// ── Observers: local cache, golden diff, action log ──
	observeResults(groups)

	if noPublish {
		log.Println("⚠️  -no-publish set: skipping Phase 3 (PR publishing)")
		return
	}

	// ── Phase 3: Walk groups and publish one PR per group with publishable items ──
	outcomes := orchestration.Publish(groups)
	orchestration.PrintPublishSummary(outcomes)
}

// observeResults runs side effects that happen once per WorkItem but are
// not part of publishing: write the local generated copy, diff against the
// golden file, and accumulate the action log.
func observeResults(groups []workplan.WorkItemGroup) {
	var actionLog []logging.ActionEntry
	for _, group := range groups {
		for _, item := range group.Items {
			actionLog = append(actionLog, logging.ActionEntry{
				Component: item.Naming.SpecImageName,
				Version:   item.Naming.VersionRevision,
				Action:    item.Result.Outcome.String(),
			})

			switch item.Result.Outcome {
			case buildresult.OutcomeSkipped:
				log.Printf("✅ PASS  %s @ %s [SKIPPED]", item.Naming.SpecImageName, item.Tag.Stripped)
				continue
			case buildresult.OutcomeFailed:
				continue
			}

			writeGenerated(item)
			diffWithGolden(item)
		}
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
