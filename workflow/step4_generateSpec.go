// ═══════════════════════════════════════════════════════════════════════════════
// Step 5 — Generate
//
//   Parses the Dockerfile and Makefile, then transforms them into a DALEC spec
//   YAML file written to the result directory.
//
//   Chunk 1 · MAIN  GenerateSpec()
// ═══════════════════════════════════════════════════════════════════════════════

package workflow

import (
	"fmt"
	"os"

	"dalec-mapping/domain/onboarding"
	domainRepo "dalec-mapping/domain/repository"
	"dalec-mapping/infrastructure/parser"
	"dalec-mapping/infrastructure/repository"
	"dalec-mapping/infrastructure/transformer"
	"dalec-mapping/utils"
)

// ─── Chunk 1 · MAIN ─────────────────────────────────────────────────────────

// GenerateSpec creates the DALEC spec from parsed build files using static
// Dockerfile analysis. Returns the resolved build target strings for downstream
// use (e.g. image test).
func GenerateSpec(onboard *onboarding.OnboardingInfo, tag string) ([]string, error) {
	// Fetch repository metadata (component path is extracted from the URL automatically)
	var (
		repoInfo *domainRepo.RepoInfo
		err      error
	)
	if repository.IsADORepo(onboard.Repository) {
		repoInfo, err = repository.FetchADORepoInfo(onboard.Repository, tag)
	} else {
		repoInfo, err = repository.FetchRepoInfo(onboard.Repository, tag)
	}
	if err != nil {
		fmt.Printf("❌ Error fetching repository info: %v\n", err)
		os.Exit(1)
	}

	// Parse Dockerfile and Makefile — sets contents.Dockerfile and contents.Makefile globals.
	specFilePath := "" // TODO: later
	previousDalecSpecInfo, err := parser.ParseOptionalFileInfo(onboard.DockerfileContent, onboard.MakefileContent, specFilePath)
	if err != nil {
		fmt.Printf("❌ Error parsing optional files: %v\n", err)
		os.Exit(1)
	}

	// Static extraction from Dockerfile AST — sets contents.Spec global.
	if parser.ExtractStaticBuildValues() != nil {
		fmt.Println("✅ Using static Dockerfile extraction")
	} else {
		fmt.Println("⚠️  Static extraction yielded no results, proceeding with defaults")
	}

	// Build the default spec from repo metadata + global Dockerfile info.
	defaultSpec, err := transformer.InitDefaultSpec(onboard, repoInfo, previousDalecSpecInfo)
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
	dalecSpec := transformer.TransformToDalec(defaultSpec)

	if err := parser.WriteOutput(dalecSpec); err != nil {
		fmt.Printf("❌ Error writing output YAML file: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("✅ Successfully generated %s\n\n", utils.ResultDir)
	return resolvedTargets, nil
}