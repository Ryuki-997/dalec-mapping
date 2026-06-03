package main

import (
	"bytes"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"dalec-mapping/domain/buildresult"
)

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
// ./generated/{component}/{component}-{tag}-{revision}-specfile.yml so it can serve as a
// cache of the latest run.
func writeGenerated(result buildresult.BuildResult) {
	component := result.Item.Naming.SpecImageName
	tag := result.Item.Tag.Stripped
	revision := result.Item.Tag.Revision
	specContent := normalizeIndent(result.SpecContent)

	generatedDir := filepath.Join("generated", component)
	if err := os.MkdirAll(generatedDir, 0o755); err != nil {
		log.Printf("⚠️  Failed to create generated directory %s: %v", generatedDir, err)
		return
	}

	fileName := fmt.Sprintf("%s-%s-%d-specfile.yml", component, tag, revision)
	generatedPath := filepath.Join(generatedDir, fileName)
	if err := os.WriteFile(generatedPath, specContent, 0o644); err != nil {
		log.Printf("⚠️  Failed to write generated spec %s: %v", generatedPath, err)
		return
	}
}

// diffWithGolden compares the generated spec content against the golden file
// at ./correct/{component}/{component}-{tag}-{revision}-specfile.yml.
// Logs PASS if identical, FAIL if not, SKIP if no golden exists. The pipeline
// emits structured log lines that test.sh parses for pass/fail accounting.
func diffWithGolden(result buildresult.BuildResult) {
	component := result.Item.Naming.SpecImageName
	tag := result.Item.Tag.Stripped
	revision := result.Item.Tag.Revision
	action := result.Outcome.String()
	specContent := normalizeIndent(result.SpecContent)

	goldenPath := filepath.Join("correct", component, fmt.Sprintf("%s-%s-%d-specfile.yml", component, tag, revision))

	goldenContent, err := os.ReadFile(goldenPath)
	if err != nil {
		log.Printf("⚠️  SKIP diff for %s @ %s [%s] — no golden file", component, tag, action)
		return
	}
	goldenContent = normalizeIndent(goldenContent)

	if string(specContent) == string(goldenContent) {
		log.Printf("✅ PASS  %s @ %s [%s]", component, tag, action)
		return
	}

	log.Printf("❌ FAIL  %s @ %s [%s] — golden mismatch", component, tag, action)
}
