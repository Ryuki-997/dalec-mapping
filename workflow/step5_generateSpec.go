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

	"dalec-mapping/infrastructure/ado"
	"dalec-mapping/infrastructure/github"
	"dalec-mapping/infrastructure/parser"
	"dalec-mapping/infrastructure/transformer"
	"dalec-mapping/pipeline"
)

// GenerateSpec creates the DALEC spec from parsed build files using static
// Dockerfile analysis. Returns the resolved build target strings for downstream
// use (e.g. image test).
func GenerateSpec() ([]string, error) {
	onboard := pipeline.Current.Onboard

	if err := fetchRepoMetadata(onboard.Repository); err != nil {
		return nil, fmt.Errorf("fetching repository info: %w", err)
	}

	if err := parseAndExtract(onboard.DockerfileContent, onboard.MakefileContent); err != nil {
		return nil, fmt.Errorf("parsing build files: %w", err)
	}

	resolvedTargets, err := buildAndWriteSpec()
	if err != nil {
		return nil, err
	}

	log.Printf("Step 5 output: targets=%v\n", resolvedTargets)
	return resolvedTargets, nil
}

// fetchRepoMetadata fetches repository metadata from GitHub or ADO based on the repo URL.
// Stores the result in pipeline.Current.RepoInfo. Populates LatestCommit from
// the tag cache and Version from the current TagSet.
func fetchRepoMetadata(repoURL string) error {
	onboard := pipeline.Current.Onboard
	var err error
	if ado.IsADORepo(repoURL) {
		pipeline.Current.RepoInfo, err = ado.FetchADORepoInfo(repoURL)
		if err != nil {
			return err
		}
		repoInfo := pipeline.Current.RepoInfo
		repoInfo.ComponentPath = ado.ResolveComponentPath(
			repoURL, onboard.DockerfileDir, onboard.MakefileDir, onboard.SpecImageName)
		repoInfo.ComponentName = onboard.SpecImageName

		tagSet := pipeline.Current.Tag
		repoInfo.Generator = ado.DetectADOGenerator(
			repoURL, repoInfo.ComponentPath, tagSet.Full)
	} else {
		pipeline.Current.RepoInfo, err = github.FetchRepoInfo(repoURL)
		if err != nil {
			return err
		}
		repoInfo := pipeline.Current.RepoInfo
		repoInfo.ComponentPath = github.ResolveComponentPath(
			repoURL, onboard.DockerfileDir, onboard.MakefileDir, onboard.SpecImageName)
		repoInfo.ComponentName = onboard.SpecImageName
	}

	tagSet := pipeline.Current.Tag
	commitSHA, err := pipeline.LookupTagCommit(repoURL, tagSet.Full)
	if err != nil {
		return fmt.Errorf("failed to resolve commit for tag %s: %w", tagSet.Full, err)
	}
	pipeline.Current.RepoInfo.LatestCommit = commitSHA
	pipeline.Current.RepoInfo.Version = tagSet.Version
	return nil
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

	return pipeline.Current.Onboard.Targets, nil
}
