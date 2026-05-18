package main

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
)

// writeGenerated writes the generated spec content to
// ./generated/{component}/{component}-{tag}-specfile.yml so it can serve as a
// cache of the latest run.
func writeGenerated(component, tag string, specContent []byte) {
	generatedDir := filepath.Join("generated", component)
	if err := os.MkdirAll(generatedDir, 0o755); err != nil {
		log.Printf("⚠️  Failed to create generated directory %s: %v", generatedDir, err)
		return
	}

	fileName := fmt.Sprintf("%s-%s-specfile.yml", component, tag)
	generatedPath := filepath.Join(generatedDir, fileName)
	if err := os.WriteFile(generatedPath, specContent, 0o644); err != nil {
		log.Printf("⚠️  Failed to write generated spec %s: %v", generatedPath, err)
		return
	}
}

// diffWithGolden compares the generated spec content against the golden file
// at ./correct/{component}/{component}-{tag}-specfile.yml.
// Logs PASS if identical, otherwise writes a unified diff to ./diff/.
func diffWithGolden(component, tag string, specContent []byte) {
	goldenPath := filepath.Join("correct", component, fmt.Sprintf("%s-%s-specfile.yml", component, tag))

	goldenContent, err := os.ReadFile(goldenPath)
	if err != nil {
		log.Printf("⚠️  SKIP diff for %s @ %s — no golden file at %s", component, tag, goldenPath)
		return
	}

	if string(specContent) == string(goldenContent) {
		log.Printf("✅ PASS  %s @ %s", component, tag)
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

	diffOutput, _ := exec.Command("diff", "-u", goldenPath, actualPath.Name()).Output()

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

	log.Printf("❌ FAIL  %s @ %s — diff written to %s", component, tag, diffPath)
}
