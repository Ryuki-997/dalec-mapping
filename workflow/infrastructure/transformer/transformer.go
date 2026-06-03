package transformer

import (
	"dalec-mapping/domain/contents"
	"dalec-mapping/domain/workplan"
	"dalec-mapping/workflow/infrastructure/parser"
)

// onboardBuildTargets returns the validated build targets as typed BuildTarget values.
func onboardBuildTargets(item *workplan.WorkItem) []contents.BuildTarget {
	targets := make([]contents.BuildTarget, len(item.Naming.Targets))
	for i, t := range item.Naming.Targets {
		targets[i] = contents.BuildTarget(t)
	}
	return targets
}

// TransformToDalec converts parsed Dockerfile info to Dalec spec format.
// Reads all inputs from item (populated incrementally by earlier Phase 2 sub-steps).
func TransformToDalec(item *workplan.WorkItem) parser.DalecSpec {
	spec := make(parser.DalecSpec)

	// Add syntax header (special comment format)
	spec["# syntax"] = "ghcr.io/project-dalec/dalec/frontend:0.20"

	// Detect pinned Go toolchain image from Dockerfile stages and store version.
	if pin := parser.DetectGoToolchainPin(item.BuildFiles.Dockerfile.Stages); pin != nil {
		item.BuildFiles.RepoInfo.GoVersion = pin.GoVersion()
	}

	// Detect go mod download patterns once — shared across build, sources, and args.
	goModDownloads := detectGoModDownloads(item)

	// Compute build section first to discover which variables are referenced
	buildSection, referencedVars := extractBuildSection(item, goModDownloads)
	spec["build"] = buildSection

	// Initialize args + metadata — only include Makefile vars that are actually used
	extractMetadata(item, spec)
	spec["args"] = extractArgs(item, referencedVars, goModDownloads)

	// Build extensions section
	spec["x-build-extensions"] = extractBuildExtensions(item)

	// Transform Dockerfile content to Dalec sections
	spec["sources"] = extractSourcesSection(item, goModDownloads)

	spec["dependencies"] = map[string]interface{}{
		"build":   map[string]interface{}{},
		"runtime": map[string]interface{}{},
	}
	spec["artifacts"] = extractArtifactsSection(item)
	spec["targets"] = extractTargetsSection(item)
	spec["tests"] = []string{}

	return spec
}
