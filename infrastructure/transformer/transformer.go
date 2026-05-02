package transformer

import (
	"dalec-mapping/domain/contents"
	"dalec-mapping/infrastructure/parser"
	"dalec-mapping/pipeline"
	"log"

	"fmt"
	"strings"
)

// ResolveBuildTargets validates the targets list from onboard.yml and replaces
// pipeline.Current.Onboard.Targets with only the valid entries. Returns an
// error if no valid targets remain.
func ResolveBuildTargets() error {
	onboardInfo := pipeline.Current.Onboard

	resolved := resolveOnboardTargets(onboardInfo.Targets)
	if len(resolved) == 0 {
		return fmt.Errorf("no valid targets in onboard.yml for %s", onboardInfo.SpecImageName)
	}

	validated := make([]string, len(resolved))
	for i, bt := range resolved {
		validated[i] = string(bt)
	}
	onboardInfo.Targets = validated

	return nil
}

// resolveOnboardTargets validates the targets list from onboard.yml and returns
// the corresponding BuildTarget values. Logs a warning for any invalid entries.
func resolveOnboardTargets(targets []string) []contents.BuildTarget {
	var resolved []contents.BuildTarget
	for _, t := range targets {
		t = strings.TrimSpace(t)
		if bt, ok := contents.IsValidTarget(t); ok {
			resolved = append(resolved, bt)
		} else {
			log.Printf("⚠️  Ignoring unsupported onboard target: %s\n", t)
		}
	}
	return resolved
}

// onboardBuildTargets returns the resolved build targets from the current
// onboard config as typed BuildTarget values.
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
	goModDownloads := DetectGoModDownloads()

	// Compute build section first to discover which variables are referenced
	buildSection, referencedVars := extractBuildSection(goModDownloads)
	spec["build"] = buildSection

	// Initialize args + metadata — only include Makefile vars that are actually used
	args := extractDefaultsSection(referencedVars, goModDownloads, spec)
	spec["args"] = args

	// Build extensions section
	spec["x-build-extensions"] = extractBuildExtensions()

	// Transform Dockerfile content to Dalec sections
	spec["sources"] = extractSourcesSection(goModDownloads)

	spec["dependencies"] = extractDependencies()
	spec["artifacts"] = extractArtifactsSection()
	spec["targets"] = extractTargetsSection()
	spec["tests"] = extractTests()

	return spec
}
