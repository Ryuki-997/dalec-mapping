package workflow

import (
	"fmt"
	"os"

	"dalec-mapping/domain/llm"
	"dalec-mapping/domain/onboarding"
	"dalec-mapping/infrastructure/github"
	"dalec-mapping/infrastructure/parser"
	"dalec-mapping/infrastructure/transformer"
	"dalec-mapping/utils"
)

// Generate runs the generation step to create dalec specs
func Generate(onboard *onboarding.OnboardingInfo, fileContents *llm.InstructionContents, agentResponse []byte) error {
	fmt.Println("=== GENERATE MODE ===")

	// Fetch GitHub repository info
	tag := ""
	if len(onboard.Tag) > 0 {
		tag = onboard.Tag[0]
	}
	repoInfo, err := github.FetchRepoInfo(onboard.Repository, tag)
	if err != nil {
		fmt.Printf("❌ Error fetching repository info: %v\n", err)
		os.Exit(1)
	}

	// TODO: later
	specFilePath := ""

	// Parse Dockerfile if path provided
	dockerfileInfo, makefileInfo, nonDeterministicInfo, previousDalecSpecInfo, err := parser.ParseOptionalFileInfo(fileContents.Dockerfile, fileContents.Makefile, specFilePath, agentResponse)
	if err != nil {
		fmt.Printf("❌ Error parsing optional files: %v\n", err)
		os.Exit(1)
	}

	// Transform to Dalec spec with repository metadata
	fmt.Println("=== TRANSFORMING TO DALEC SPEC ===")

	defaultSpec := transformer.InitDefaultSpec(onboard, repoInfo, &dockerfileInfo, previousDalecSpecInfo)

	fmt.Println("=== DOCKER FILE INFO ===")
	transformer.PrintDockerfileInfo(defaultSpec)

	dalecSpec := transformer.TransformToDalec(defaultSpec, &makefileInfo, nonDeterministicInfo)

	// Write to output file
	fmt.Println("=== WRITING OUTPUT YAML FILE ===")

	err = parser.WriteOutput(dalecSpec)
	if err != nil {
		fmt.Printf("❌ Error writing output YAML file: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("✅ Successfully generated %s\n\n", utils.ResultDir)

	return nil
}