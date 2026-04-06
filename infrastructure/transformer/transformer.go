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

func InitDefaultSpec(onboardInfo *onboarding.OnboardingInfo, repoInfo *repository.RepoInfo, dockerfileInfo *contents.DockerfileInfo, previousDalecSpecInfo parser.PreviousDalecSpec) *contents.DefaultSpec {
	// Initialize & Populate Source of Truth Attributes from onboarding and repository info
	defaultSpec := &contents.DefaultSpec{}
	defaultSpec.RepoInfo = *repoInfo

	defaultSpec.OnboardingInfo = *onboardInfo

	if dockerfileInfo != nil {
		defaultSpec.DockerfileInfo = *dockerfileInfo
	}

	defaultSpec.Revision = 1

	// Default Build Targets (can be overridden by LLM-extracted targets)
	defaultSpec.BuildTargets = []contents.BuildTarget{
		contents.AzLinux3Container,
		contents.WindowsCrossContainer,
	}

	return defaultSpec
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

// ResolveTargets validates and converts TargetSpec values from NonDeterministicValues
// into BuildTarget values. Returns nil if none provided (caller uses InitDefaultSpec defaults).
func ResolveTargets(targets []llm.TargetSpec) []contents.BuildTarget {
	if len(targets) == 0 {
		fmt.Println("ℹ️  No targets specified, using InitDefaultSpec defaults")
		return nil
	}

	resolved := []contents.BuildTarget{}
	for _, ts := range targets {
		name := strings.TrimSpace(ts.TargetOS)
		if bt, ok := contents.IsValidTarget(name); ok {
			resolved = append(resolved, bt)
		} else {
			fmt.Printf("⚠️  Ignoring unsupported target: %s\n", name)
		}
	}

	if len(resolved) == 0 {
		fmt.Println("⚠️  No valid targets found, keeping InitDefaultSpec defaults")
		return nil
	}

	return resolved
}
