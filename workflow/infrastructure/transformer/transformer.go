package transformer

import (
	"dalec-mapping/domain/contents"
	"dalec-mapping/domain/workplan"
	"dalec-mapping/workflow/infrastructure/parser"
)

// onboardBuildTargets returns the validated build targets as typed BuildTarget values.
func onboardBuildTargets(component *workplan.WorkComponent) []contents.BuildTarget {
	targets := make([]contents.BuildTarget, len(component.Group.Targets))
	for i, t := range component.Group.Targets {
		targets[i] = contents.BuildTarget(t)
	}
	return targets
}

// TransformToDalec converts parsed Dockerfile info to Dalec spec format.
// Reads all inputs from component (populated incrementally by earlier Phase 2
// sub-steps); group-level config (Targets, License — onboard.yml inputs
// that are constant across every WorkComponent in the group) is read via
// component.Group.
func TransformToDalec(component *workplan.WorkComponent) parser.DalecSpec {
	spec := make(parser.DalecSpec)

	// Add syntax header (special comment format)
	spec["# syntax"] = "ghcr.io/project-dalec/dalec/frontend:0.20"

	// Detect go mod download patterns once — shared across build, sources, and args.
	goModDownloads := detectGoModDownloads(component)

	// Compute build section first to discover which variables are referenced
	buildSection, referencedVars := extractBuildSection(component, goModDownloads)
	spec["build"] = buildSection

	// Initialize args + metadata — only include Makefile vars that are actually used
	extractMetadata(component, spec)
	spec["args"] = extractArgs(component, referencedVars, goModDownloads)

	// Build extensions section
	spec["x-build-extensions"] = extractBuildExtensions(component)

	// Transform Dockerfile content to Dalec sections
	spec["sources"] = extractSourcesSection(component, goModDownloads)

	spec["dependencies"] = map[string]interface{}{
		"build":   map[string]interface{}{},
		"runtime": map[string]interface{}{},
	}
	spec["artifacts"] = extractArtifactsSection(component)
	spec["targets"] = extractTargetsSection(component)
	spec["tests"] = []string{}

	return spec
}
