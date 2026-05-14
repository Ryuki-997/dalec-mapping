package transformer

import (
	"dalec-mapping/domain/contents"
	"dalec-mapping/infrastructure/parser"
	"dalec-mapping/pipeline"
	"log"

	"fmt"
	"strings"
)

// ResolveBuildTargets validates the targets list from onboard.yml, overwrites
// Targets with only the valid entries, and returns an error if none remain.
func ResolveBuildTargets() error {
	onboardInfo := pipeline.Current.Onboard

	var resolved []string
	for _, t := range onboardInfo.Targets {
		t = strings.TrimSpace(t)
		if _, ok := contents.IsValidTarget(t); ok {
			resolved = append(resolved, t)
		} else {
			log.Printf("⚠️  Ignoring unsupported onboard target: %s\n", t)
		}
	}
	if len(resolved) == 0 {
		return fmt.Errorf("no valid targets in onboard.yml for %s", onboardInfo.SpecImageName)
	}

	onboardInfo.Targets = resolved
	return nil
}

// onboardBuildTargets returns the validated build targets as typed BuildTarget values.
func onboardBuildTargets() []contents.BuildTarget {
	targets := make([]contents.BuildTarget, len(pipeline.Current.Onboard.Targets))
	for i, t := range pipeline.Current.Onboard.Targets {
		targets[i] = contents.BuildTarget(t)
	}
	return targets
}

// TransformToDalec converts parsed Dockerfile info to Dalec spec format.
// Reads all inputs from pipeline.Current.
func TransformToDalec() parser.DalecSpec {
	spec := make(parser.DalecSpec)

	// Add syntax header (special comment format)
	spec["# syntax"] = "ghcr.io/project-dalec/dalec/frontend:0.20"

	// Detect pinned Go toolchain image from Dockerfile stages and store version.
	if pin := parser.DetectGoToolchainPin(pipeline.Current.Dockerfile.Stages); pin != nil {
		pipeline.Current.RepoInfo.GoVersion = pin.GoVersion()
	}

	// Detect go mod download patterns once — shared across build, sources, and args.
	goModDownloads := detectGoModDownloads()

	// Compute build section first to discover which variables are referenced
	buildSection, referencedVars := extractBuildSection(goModDownloads)
	spec["build"] = buildSection

	// Initialize args + metadata — only include Makefile vars that are actually used
	extractMetadata(spec)
	spec["args"] = extractArgs(referencedVars, goModDownloads)

	// Build extensions section
	spec["x-build-extensions"] = extractBuildExtensions()

	// Transform Dockerfile content to Dalec sections
	spec["sources"] = extractSourcesSection(goModDownloads)

	spec["dependencies"] = map[string]interface{}{
		"build":   map[string]interface{}{},
		"runtime": map[string]interface{}{},
	}
	spec["artifacts"] = extractArtifactsSection()
	spec["targets"] = extractTargetsSection()
	spec["tests"] = []string{}

	return spec
}
