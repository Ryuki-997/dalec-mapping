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
//   Three explicit phases over a single []workplan.WorkGroup:
//
//     Phase 1  Resolve  ()                            → []workplan.WorkGroup
//     Phase 2  Generate []workplan.WorkGroup          (mutates each component.Result in place)
//     Phase 3  Publish  []workplan.WorkGroup          → []orchestration.PublishOutcome
//
//   Per-spec side effects (golden diff, local cache write, action log) live
//   between phases 2 and 3 as pure observers walking groups → component.Result.
// ═══════════════════════════════════════════════════════════════════════════════

func main() {
	inputPath, patchMode, noPublish := orchestration.ParseFlags()
	orchestration.LoadEnv()

	if patchMode {
		runPatchWorkflow()
		return
	}

	// ── Phase 1: Resolve onboard files and tag patterns into grouped work components ──
	groups := orchestration.Resolve(inputPath)

	// ── Phase 2: Generate spec per component (writes component.Result; no PR/grouping logic) ──
	orchestration.Generate(groups)

	// ── Observers: local cache, golden diff, action log ──
	observeResults(groups)

	if noPublish {
		log.Println("⚠️  -no-publish set: skipping Phase 3 (PR publishing)")
		return
	}

	// ── Phase 3: Walk groups and publish one PR per group with publishable components ──
	outcomes := orchestration.Publish(groups)
	orchestration.PrintPublishSummary(outcomes)
}

// observeResults runs side effects that happen once per WorkComponent but are
// not part of publishing: write the local generated copy, diff against the
// golden file, and accumulate the action log.
func observeResults(groups []workplan.WorkGroup) {
	var actionLog []logging.ActionEntry
	for _, group := range groups {
		for _, component := range group.Components {
			actionLog = append(actionLog, logging.ActionEntry{
				Component: component.Naming.SpecImageName,
				Version:   component.Naming.VersionRevision,
				Action:    component.Result.Outcome.String(),
			})

			switch component.Result.Outcome {
			case buildresult.OutcomeSkipped:
				log.Printf("✅ PASS  %s @ %s [SKIPPED]", component.Naming.SpecImageName, component.Tag.Stripped)
				continue
			case buildresult.OutcomeFailed:
				continue
			}

			writeGenerated(component)
			diffWithGolden(component)
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
