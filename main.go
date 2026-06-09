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
	flags := orchestration.ParseFlags()
	initInfo := orchestration.LoadEnv(flags)
	logging.PrintInitBanner(initInfo)

	if flags.PatchMode {
		runPatchWorkflow()
		return
	}

	// ── Phase 1: Resolve onboard files and tag patterns into grouped work components ──
	groups := orchestration.Resolve(flags.InputPath)

	// ── Phase 2: Generate spec per component (writes component.Result; no PR/grouping logic) ──
	orchestration.Generate(groups)

	// ── Observers: local cache, golden diff, action log accumulation ──
	actionLog, testResults := observeResults(groups)

	// ── Phase 3: Walk groups and publish one PR per group with publishable components ──
	var outcomes []orchestration.PublishOutcome
	if !flags.NoPublish {
		outcomes = orchestration.Publish(groups)
	}

	// ── Finalization ──
	logging.PrintFinalizationBanner()
	logging.PrintActionLog(actionLog)
	printTestResults(testResults)
	if flags.NoPublish {
		log.Println("  PR Summary: skipped (-no-publish)")
		return
	}
	orchestration.PrintPublishSummary(outcomes)
}

// observeResults runs side effects that happen once per WorkComponent but are
// not part of publishing: write the local generated copy, diff against the
// golden file, and accumulate both the action log and the per-test results.
// Per-test ✅ PASS / ❌ FAIL / ⚠️ SKIP lines are still emitted inline because
// test.sh greps them for pass/fail accounting.
func observeResults(groups []workplan.WorkGroup) ([]logging.ActionEntry, []testResult) {
	var actionLog []logging.ActionEntry
	var results []testResult
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
				results = append(results, testResult{
					Component: component.Naming.SpecImageName,
					Tag:       component.Tag.Stripped,
					Action:    "SKIPPED",
					Status:    testPass,
				})
				continue
			case buildresult.OutcomeFailed:
				continue
			}

			writeGenerated(component)
			results = append(results, diffWithGolden(component))
		}
	}
	return actionLog, results
}

// printTestResults renders the aggregated test summary under the Finalization
// banner. Skipped count is omitted when zero; failures are listed inline.
func printTestResults(results []testResult) {
	if len(results) == 0 {
		return
	}
	var passed, failed, skipped int
	for _, result := range results {
		switch result.Status {
		case testPass:
			passed++
		case testFail:
			failed++
		case testSkip:
			skipped++
		}
	}
	if skipped == 0 {
		log.Printf("  Test Results: %d passed, %d failed", passed, failed)
	} else {
		log.Printf("  Test Results: %d passed, %d failed, %d skipped", passed, failed, skipped)
	}
	for _, result := range results {
		if result.Status != testFail {
			continue
		}
		log.Printf("    ❌ %s @ %s [%s] — %s", result.Component, result.Tag, result.Action, result.Reason)
	}
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
