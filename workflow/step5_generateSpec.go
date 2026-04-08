// ═══════════════════════════════════════════════════════════════════════════════
// Step 5 — Generate
//
//   Parses the Dockerfile, Makefile, and LLM-extracted values, then transforms
//   them into a DALEC spec YAML file written to the result directory.
//
//   Chunk 1 · MAIN  GenerateSpec()
// ═══════════════════════════════════════════════════════════════════════════════

package workflow

import (
	"fmt"
	"os"
	"path"

	"dalec-mapping/domain/onboarding"
	"dalec-mapping/infrastructure/parser"
	"dalec-mapping/infrastructure/repository"
	"dalec-mapping/infrastructure/transformer"
	"dalec-mapping/utils"
)

// ─── Chunk 1 · MAIN ─────────────────────────────────────────────────────────

// GenerateSpec creates the DALEC spec from parsed build files.
// Uses static Dockerfile analysis as the primary extraction method.
// The agentResponse (LLM output) is used as a fallback when static extraction
// yields no results.
// Returns the resolved build target strings for downstream use (e.g. image test).
func GenerateSpec(onboard *onboarding.OnboardingInfo, agentResponse []byte, tag string) ([]string, error) {
	// Fetch repository metadata
	subdir := path.Dir(onboard.DockerfileDir)
	repoInfo, err := repository.FetchRepoInfo(onboard.Repository, subdir, tag)
	if err != nil {
		fmt.Printf("❌ Error fetching repository info: %v\n", err)
		os.Exit(1)
	}

	// Parse Dockerfile, Makefile, and extract build values
	specFilePath := "" // TODO: later
	dockerfileInfo, makefileInfo, nonDeterministicInfo, previousDalecSpecInfo, err := parser.ParseOptionalFileInfo(onboard.DockerfileContent, onboard.MakefileContent, specFilePath, agentResponse)
	if err != nil {
		fmt.Printf("❌ Error parsing optional files: %v\n", err)
		os.Exit(1)
	}

	// Prefer static extraction over LLM output.
	if staticNDV := parser.ExtractStaticBuildValues(dockerfileInfo.Stages, dockerfileInfo.Args); staticNDV != nil {
		fmt.Println("✅ Using static Dockerfile extraction (LLM bypassed)")
		nonDeterministicInfo = staticNDV
	} else if nonDeterministicInfo != nil {
		fmt.Println("⚠️  Static extraction yielded no results, falling back to LLM output")
	}

	// Build the default spec from repo metadata + Dockerfile info
	defaultSpec, err := transformer.InitDefaultSpec(onboard, repoInfo, &dockerfileInfo, previousDalecSpecInfo)
	if err != nil {
		return nil, err
	}

	// Collect resolved target strings for downstream use
	resolvedTargets := make([]string, len(defaultSpec.BuildTargets))
	for i, bt := range defaultSpec.BuildTargets {
		resolvedTargets[i] = string(bt)
	}

	parser.PrintDockerfileInfo(defaultSpec)

	// Transform to final DALEC spec and write output
	dalecSpec := transformer.TransformToDalec(defaultSpec, &makefileInfo, nonDeterministicInfo)

	if err := parser.WriteOutput(dalecSpec); err != nil {
		fmt.Printf("❌ Error writing output YAML file: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("✅ Successfully generated %s\n\n", utils.ResultDir)
	return resolvedTargets, nil
}