// ═══════════════════════════════════════════════════════════════════════════════
// Step 5 — Generate
//
//   Parses the Dockerfile and Makefile, then transforms them into a DALEC spec
//   YAML file written to the result directory.
//
//   Chunk 1 · MAIN   GenerateSpec()
//   Chunk 2 · STEPS  fetchRepoMetadata(), parseAndExtract(), buildAndWriteSpec()
// ═══════════════════════════════════════════════════════════════════════════════

package workflow

import (
	"log"
	"os"

	"dalec-mapping/infrastructure/parser"
	"dalec-mapping/infrastructure/repository"
	"dalec-mapping/infrastructure/transformer"
	"dalec-mapping/pipeline"
	"dalec-mapping/utils"
)

// ─── Chunk 1 · MAIN ─────────────────────────────────────────────────────────

// GenerateSpec creates the DALEC spec from parsed build files using static
// Dockerfile analysis. Returns the resolved build target strings for downstream
// use (e.g. image test).
func GenerateSpec() ([]string, error) {
	if err := fetchRepoMetadata(); err != nil {
		log.Printf("❌ Error fetching repository info: %v\n", err)
		os.Exit(1)
	}

	if err := parseAndExtract(); err != nil {
		log.Printf("❌ Error parsing optional files: %v\n", err)
		os.Exit(1)
	}

	resolvedTargets, err := buildAndWriteSpec()
	if err != nil {
		return nil, err
	}

	return resolvedTargets, nil
}

// ─── Chunk 2 · STEPS ─────────────────────────────────────────────────────────

// fetchRepoMetadata fetches repository metadata from GitHub or ADO based on the repo URL.
// Stores the result in pipeline.Current.RepoInfo.
func fetchRepoMetadata() error {
	onboard := pipeline.Current.Onboard
	tag := pipeline.Current.Tag

	var err error
	if repository.IsADORepo(onboard.Repository) {
		pipeline.Current.RepoInfo, err = repository.FetchADORepoInfo(onboard.Repository, tag)
	} else {
		pipeline.Current.RepoInfo, err = repository.FetchRepoInfo(onboard.Repository, tag)
	}
	return err
}

// parseAndExtract parses Dockerfile/Makefile content and runs static extraction.
// Stores the previous spec info in pipeline.Current.PreviousSpec.
func parseAndExtract() error {
	onboard := pipeline.Current.Onboard
	specFilePath := "" // TODO: later

	previousDalecSpecInfo, err := parser.ParseOptionalFileInfo(onboard.DockerfileContent, onboard.MakefileContent, specFilePath)
	if err != nil {
		return err
	}
	pipeline.Current.PreviousSpec = previousDalecSpecInfo

	if parser.ExtractStaticBuildValues() != nil {
		log.Println("✅ Using static Dockerfile extraction")
	} else {
		log.Println("⚠️  Static extraction yielded no results, proceeding with defaults")
	}

	return nil
}

// buildAndWriteSpec builds the default spec, transforms it to a DALEC spec, and writes the output.
func buildAndWriteSpec() ([]string, error) {
	defaultSpec, err := transformer.InitDefaultSpec()
	if err != nil {
		return nil, err
	}
	pipeline.Current.DefaultSpec = defaultSpec

	resolvedTargets := make([]string, len(defaultSpec.BuildTargets))
	for i, buildTarget := range defaultSpec.BuildTargets {
		resolvedTargets[i] = string(buildTarget)
	}

	parser.PrintDockerfileInfo(defaultSpec)

	dalecSpec := transformer.TransformToDalec(defaultSpec)

	if err := parser.WriteOutput(dalecSpec); err != nil {
		log.Printf("❌ Error writing output YAML file: %v\n", err)
		os.Exit(1)
	}

	log.Printf("✅ Successfully generated %s\n\n", utils.ResultDir)
	return resolvedTargets, nil
}
