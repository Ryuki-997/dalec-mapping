package main

import (
	"bytes"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
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
func writeGenerated(component, tag string, revision int, specContent []byte) {
	specContent = normalizeIndent(specContent)
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
// Logs PASS if identical, otherwise writes a unified diff to ./diff/.
func diffWithGolden(component, tag string, revision int, action string, specContent []byte) {
	specContent = normalizeIndent(specContent)
	goldenPath := filepath.Join("correct", component, fmt.Sprintf("%s-%s-%d-specfile.yml", component, tag, revision))

	goldenContent, err := os.ReadFile(goldenPath)
	if err != nil {
		log.Printf("⚠️  SKIP diff for %s @ %s [%s] — no golden file at %s", component, tag, action, goldenPath)
		return
	}
	goldenContent = normalizeIndent(goldenContent)

	if string(specContent) == string(goldenContent) {
		log.Printf("✅ PASS  %s @ %s [%s]", component, tag, action)
		return
	}

	// Write actual output to a temp file for diff
	actualPath, err := os.CreateTemp("", "spec-actual-*.yml")
	if err != nil {
		log.Printf("⚠️  Failed to create temp file for diff: %v", err)
		return
	}
	defer os.Remove(actualPath.Name())

	if _, err := actualPath.Write(specContent); err != nil {
		actualPath.Close()
		log.Printf("⚠️  Failed to write temp file for diff: %v", err)
		return
	}
	actualPath.Close()

	// Write normalized golden to a temp file so diff output shows only content differences
	goldenTmp, err := os.CreateTemp("", "spec-golden-*.yml")
	if err != nil {
		log.Printf("⚠️  Failed to create temp file for golden diff: %v", err)
		return
	}
	defer os.Remove(goldenTmp.Name())

	if _, err := goldenTmp.Write(goldenContent); err != nil {
		goldenTmp.Close()
		log.Printf("⚠️  Failed to write golden temp file for diff: %v", err)
		return
	}
	goldenTmp.Close()

	diffOutput, _ := exec.Command("diff", "-u", goldenTmp.Name(), actualPath.Name()).Output()

	if err := os.MkdirAll("diff", 0o755); err != nil {
		log.Printf("⚠️  Failed to create diff directory: %v", err)
		return
	}

	diffFileName := fmt.Sprintf("%s-%s.diff", component, tag)
	diffPath := filepath.Join("diff", diffFileName)
	if err := os.WriteFile(diffPath, diffOutput, 0o644); err != nil {
		log.Printf("⚠️  Failed to write diff file: %v", err)
		return
	}

	log.Printf("❌ FAIL  %s @ %s [%s] — diff written to %s", component, tag, action, diffPath)
}
