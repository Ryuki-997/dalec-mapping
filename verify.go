package main

import (
	"bytes"
	"log"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"dalec-mapping/domain/workplan"
)

// testStatus is the three-state outcome of a single golden-diff check.
type testStatus int

const (
	testPass testStatus = iota
	testFail
	testSkip
)

// testResult is a single record accumulated by observeResults and rendered
// at finalization by printTestResults.
type testResult struct {
	Component string
	Tag       string
	Action    string
	Status    testStatus
	Reason    string // empty for pass
}

// normalizeIndent round-trips spec content through a yaml encoder with 2-space
// indentation to ensure consistent formatting regardless of the source code path.
func normalizeIndent(content []byte) []byte {
	raw := string(content)
	var header string
	body := raw
	if strings.HasPrefix(raw, "# syntax=") {
		idx := strings.Index(raw, "\n")
		header = raw[:idx+1]
		body = raw[idx+1:]
	}

	var node yaml.Node
	if err := yaml.Unmarshal([]byte(body), &node); err != nil {
		return content
	}

	var buf bytes.Buffer
	encoder := yaml.NewEncoder(&buf)
	encoder.SetIndent(2)
	if err := encoder.Encode(&node); err != nil {
		return content
	}
	encoder.Close()

	out := strings.TrimPrefix(buf.String(), "---\n")
	return []byte(header + out)
}

// writeGenerated writes the generated spec content to
// ./generated/{component}/{SpecFileName} so it can serve as a
// cache of the latest run.
func writeGenerated(component *workplan.WorkComponent) {
	naming := component.Naming
	specContent := normalizeIndent(component.Result.SpecContent)

	generatedDir := filepath.Join("generated", naming.SpecImageName)
	if err := os.MkdirAll(generatedDir, 0o755); err != nil {
		log.Printf("⚠️  Failed to create generated directory %s: %v", generatedDir, err)
		return
	}

	generatedPath := filepath.Join(generatedDir, naming.SpecFileName)
	if err := os.WriteFile(generatedPath, specContent, 0o644); err != nil {
		log.Printf("⚠️  Failed to write generated spec %s: %v", generatedPath, err)
		return
	}
}

// diffWithGolden compares the generated spec content against the golden file
// at ./correct/{component}/{SpecFileName} and returns the recorded outcome.
// Emits structured `✅ PASS` / `❌ FAIL` / `⚠️  SKIP diff` lines that test.sh
// greps for per-test accounting; the returned testResult is what main rolls
// up into the aggregated `Test Results:` summary at finalization.
func diffWithGolden(component *workplan.WorkComponent) testResult {
	naming := component.Naming
	tag := component.Tag.Stripped
	action := component.Result.Outcome.String()
	specContent := normalizeIndent(component.Result.SpecContent)

	goldenPath := filepath.Join("correct", naming.SpecImageName, naming.SpecFileName)

	goldenContent, err := os.ReadFile(goldenPath)
	if err != nil {
		log.Printf("⚠️  SKIP diff for %s @ %s [%s] — no golden file", naming.SpecImageName, tag, action)
		return testResult{Component: naming.SpecImageName, Tag: tag, Action: action, Status: testSkip, Reason: "no golden file"}
	}
	goldenContent = normalizeIndent(goldenContent)

	if string(specContent) == string(goldenContent) {
		log.Printf("✅ PASS  %s @ %s [%s]", naming.SpecImageName, tag, action)
		return testResult{Component: naming.SpecImageName, Tag: tag, Action: action, Status: testPass}
	}

	log.Printf("❌ FAIL  %s @ %s [%s] — golden mismatch", naming.SpecImageName, tag, action)
	return testResult{Component: naming.SpecImageName, Tag: tag, Action: action, Status: testFail, Reason: "golden mismatch"}
}
