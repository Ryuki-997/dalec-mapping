// ═══════════════════════════════════════════════════════════════════════════════
// Step 5 — Generate
//
//   Parses the Dockerfile and Makefile, then transforms them into a DALEC spec
//   YAML file written to the result directory.
//
//   Functions are ordered by call sequence:
//     GenerateSpec()
//       → fetchRepoMetadata()
//       → parseAndExtract()
//       → buildAndWriteSpec()
// ═══════════════════════════════════════════════════════════════════════════════

package workflow

import (
	"fmt"
	"log"

	"dalec-mapping/infrastructure/parser"
	"dalec-mapping/infrastructure/repository"
	"dalec-mapping/infrastructure/transformer"
	"dalec-mapping/pipeline"
	"dalec-mapping/utils"
)

// GenerateSpec creates the DALEC spec from parsed build files using static
// Dockerfile analysis. Returns the resolved build target strings for downstream
// use (e.g. image test).
func GenerateSpec() ([]string, error) {
	onboard := pipeline.Current.Onboard
	tag := pipeline.Current.Tag.Full

	if err := fetchRepoMetadata(onboard.Repository, tag); err != nil {
		return nil, fmt.Errorf("fetching repository info: %w", err)
	}

	if err := parseAndExtract(onboard.DockerfileContent, onboard.MakefileContent); err != nil {
		return nil, fmt.Errorf("parsing build files: %w", err)
	}

	resolvedTargets, err := buildAndWriteSpec()
	if err != nil {
		return nil, err
	}

	return resolvedTargets, nil
}

// fetchRepoMetadata fetches repository metadata from GitHub or ADO based on the repo URL.
// Stores the result in pipeline.Current.RepoInfo.
func fetchRepoMetadata(repoURL, tag string) error {
	var err error
	if repository.IsADORepo(repoURL) {
		pipeline.Current.RepoInfo, err = repository.FetchADORepoInfo(repoURL, tag)
	} else {
		pipeline.Current.RepoInfo, err = repository.FetchRepoInfo(repoURL, tag)
	}
	return err
}

// parseAndExtract parses Dockerfile/Makefile content and runs static extraction.
func parseAndExtract(dockerfileContent, makefileContent []byte) error {
	if dockerfileContent == nil && makefileContent == nil {
		log.Println("⚠️  No Dockerfile or Makefile content to parse, proceeding with defaults")
		return nil
	}

	parser.ParseOptionalFileInfo(dockerfileContent, makefileContent)

	parser.ExtractStaticBuildValues()

	return nil
}

// buildAndWriteSpec resolves build targets, transforms to a DALEC spec, and writes the output.
func buildAndWriteSpec() ([]string, error) {
	if err := transformer.ResolveBuildTargets(); err != nil {
		return nil, err
	}

	dalecSpec := transformer.TransformToDalec()

	if err := parser.WriteOutput(dalecSpec); err != nil {
		return nil, fmt.Errorf("writing output YAML file: %w", err)
	}

	log.Printf("✅ Successfully generated %s\n\n", utils.ResultDir)
	return pipeline.Current.Onboard.Targets, nil
}
