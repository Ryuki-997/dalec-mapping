package transformer

import (
	"dalec-mapping/domain/contents"
	"dalec-mapping/domain/llm"
	"dalec-mapping/domain/onboarding"
	"dalec-mapping/domain/repository"
	"dalec-mapping/infrastructure/parser"

	"fmt"
	"strings"
)

func InitDefaultSpec(onboardInfo *onboarding.OnboardingInfo, repoInfo *repository.RepoInfo, dockerfileInfo *contents.DockerfileInfo, previousDalecSpecInfo parser.PreviousDalecSpec) (*contents.DefaultSpec, error) {
	// Initialize & Populate Source of Truth Attributes from onboarding and repository info
	defaultSpec := &contents.DefaultSpec{}
	defaultSpec.RepoInfo = *repoInfo

	defaultSpec.OnboardingInfo = *onboardInfo

	if dockerfileInfo != nil {
		defaultSpec.DockerfileInfo = *dockerfileInfo
	}

	defaultSpec.Revision = 1

	// Resolve build targets from onboard.yml (required field).
	defaultSpec.BuildTargets = resolveOnboardTargets(onboardInfo.Targets)
	if len(defaultSpec.BuildTargets) == 0 {
		return nil, fmt.Errorf("no valid targets in onboard.yml for %s", onboardInfo.SpecImageName)
	}

	return defaultSpec, nil
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
			fmt.Printf("\u26a0\ufe0f  Ignoring unsupported onboard target: %s\n", t)
		}
	}
	return resolved
}

// TransformToDalec converts parsed Dockerfile info to Dalec spec format
func TransformToDalec(defaultSpec *contents.DefaultSpec, makefileInfo *contents.MakefileInfo, nonDeterministicValues *llm.NonDeterministicValues) parser.DalecSpec {
	spec := make(parser.DalecSpec)

	// Add syntax header (special comment format)
	spec["# syntax"] = "ghcr.io/azure/dalec/frontend:latest"

	// Detect pinned Go toolchain image from Dockerfile stages and store version.
	if pin := parser.DetectGoToolchainPin(defaultSpec.Stages); pin != nil {
		defaultSpec.GoVersion = pin.GoVersion()
		fmt.Printf("🔧 Go toolchain pin detected: %s (version: %s)\n", pin.ImageRef, defaultSpec.GoVersion)
	}

	// Detect go mod download patterns once — shared across build, sources, and args.
	goModDownloads := DetectGoModDownloads(defaultSpec, nonDeterministicValues)

	// Compute build section first to discover which variables are referenced
	buildSection, referencedVars := extractBuildSection(defaultSpec, makefileInfo, nonDeterministicValues, goModDownloads)
	spec["build"] = buildSection

	// Initialize args + metadata — only include Makefile vars that are actually used
	args := extractDefaultsSection(defaultSpec, makefileInfo, referencedVars, goModDownloads, spec)
	spec["args"] = args

	// Build extensions section
	spec["x-build-extensions"] = extractBuildExtensions(defaultSpec)

	// Transform Dockerfile content to Dalec sections
	spec["sources"] = extractSourcesSection(defaultSpec, nonDeterministicValues, goModDownloads)

	spec["dependencies"] = extractDependencies()
	spec["artifacts"] = extractArtifactsSection(defaultSpec, nonDeterministicValues)
	spec["targets"] = extractTargetsSection(defaultSpec, nonDeterministicValues)
	spec["tests"] = extractTests()

	return spec
}
