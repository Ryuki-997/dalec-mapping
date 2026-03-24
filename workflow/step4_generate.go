package workflow

import (
	"fmt"
	"os"
	"path"

	"dalec-mapping/domain/onboarding"
	"dalec-mapping/infrastructure/github"
	"dalec-mapping/infrastructure/parser"
	"dalec-mapping/infrastructure/transformer"
	"dalec-mapping/utils"
)

// Generate runs the generation step to create dalec specs.
// Returns the resolved build target strings for downstream use (e.g. image test).
func Generate(onboard *onboarding.OnboardingInfo, agentResponse []byte, tag string) ([]string, error) {
	fmt.Println("=== GENERATE MODE ===")
	subdir := path.Dir(onboard.DockerfileDir)

	// Fetch GitHub repository info
	repoInfo, err := github.FetchRepoInfo(onboard.Repository, subdir, tag)
	if err != nil {
		fmt.Printf("❌ Error fetching repository info: %v\n", err)
		os.Exit(1)
	}

	// TODO: later
	specFilePath := ""

	// Parse Dockerfile if path provided
	dockerfileInfo, makefileInfo, nonDeterministicInfo, previousDalecSpecInfo, err := parser.ParseOptionalFileInfo(onboard.DockerfileContent, onboard.MakefileContent, specFilePath, agentResponse)
	if err != nil {
		fmt.Printf("❌ Error parsing optional files: %v\n", err)
		os.Exit(1)
	}

	// Transform to Dalec spec with repository metadata
	fmt.Println("=== TRANSFORMING TO DALEC SPEC ===")

	defaultSpec := transformer.InitDefaultSpec(onboard, repoInfo, &dockerfileInfo, previousDalecSpecInfo)

	// Override build targets from LLM-extracted values (keep InitDefaultSpec defaults if nil)
	if nonDeterministicInfo != nil {
		if resolved := transformer.ResolveTargets(nonDeterministicInfo.Targets); resolved != nil {
			defaultSpec.BuildTargets = resolved
		}
	}

	// Collect resolved target strings for downstream use
	resolvedTargets := make([]string, len(defaultSpec.BuildTargets))
	for i, bt := range defaultSpec.BuildTargets {
		resolvedTargets[i] = string(bt)
	}

	fmt.Println("=== DOCKER FILE INFO ===")
	parser.PrintDockerfileInfo(defaultSpec)

	dalecSpec := transformer.TransformToDalec(defaultSpec, &makefileInfo, nonDeterministicInfo)

	// Write to output file
	fmt.Println("=== WRITING OUTPUT YAML FILE ===")

	err = parser.WriteOutput(dalecSpec)
	if err != nil {
		fmt.Printf("❌ Error writing output YAML file: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("✅ Successfully generated %s\n\n", utils.ResultDir)

	return resolvedTargets, nil
}