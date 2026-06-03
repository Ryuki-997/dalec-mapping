// ═══════════════════════════════════════════════════════════════════════════════
// Spec — Generate
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

package spec

import (
	"fmt"
	"log"

	"dalec-mapping/domain/tagcache"
	"dalec-mapping/domain/workplan"
	"dalec-mapping/workflow/infrastructure/ado"
	"dalec-mapping/workflow/infrastructure/github"
	"dalec-mapping/workflow/infrastructure/parser"
	"dalec-mapping/workflow/infrastructure/transformer"
)

// GenerateSpec creates the DALEC spec from parsed build files using static
// Dockerfile analysis. Returns the encoded spec bytes and resolved build target
// strings for downstream use (e.g. image test).
func GenerateSpec(item *workplan.WorkItem) ([]byte, []string, error) {
	subject := item.Naming

	if err := fetchRepoMetadata(item, subject.Repository); err != nil {
		return nil, nil, fmt.Errorf("fetching repository info: %w", err)
	}

	if err := parseAndExtract(item, item.BuildFiles.Dockerfile.Source, item.BuildFiles.Makefile.Source); err != nil {
		return nil, nil, fmt.Errorf("parsing build files: %w", err)
	}

	specBytes, resolvedTargets, err := buildSpec(item)
	if err != nil {
		return nil, nil, err
	}

	log.Printf("Output: targets=%v\n", resolvedTargets)
	return specBytes, resolvedTargets, nil
}

// fetchRepoMetadata fetches repository metadata from GitHub or ADO based on the repo URL.
// Stores the result in item.BuildFiles.RepoInfo. Populates LatestCommit from
// the tag cache and Version from the current TagSet.
func fetchRepoMetadata(item *workplan.WorkItem, repoURL string) error {
	subject := item.Naming
	var err error
	if ado.IsADORepo(repoURL) {
		item.BuildFiles.RepoInfo, err = ado.FetchADORepoInfo(repoURL, subject.License)
		if err != nil {
			return err
		}
		repoInfo := item.BuildFiles.RepoInfo
		repoInfo.ComponentName = subject.SpecImageName

		tagSet := item.Tag
		repoInfo.Generator = ado.DetectADOGenerator(
			repoURL, repoInfo.ComponentPath, tagSet.Full)
	} else {
		item.BuildFiles.RepoInfo, err = github.FetchRepoInfo(repoURL, subject.License)
		if err != nil {
			return err
		}
		repoInfo := item.BuildFiles.RepoInfo
		repoInfo.ComponentName = subject.SpecImageName
	}

	tagSet := item.Tag
	commitSHA, err := tagcache.Lookup(repoURL, tagSet.Full)
	if err != nil {
		return fmt.Errorf("failed to resolve commit for tag %s: %w", tagSet.Full, err)
	}
	item.BuildFiles.RepoInfo.LatestCommit = commitSHA
	item.BuildFiles.RepoInfo.Version = tagSet.Version
	return nil
}

// parseAndExtract parses Dockerfile/Makefile content and runs static extraction.
func parseAndExtract(item *workplan.WorkItem, dockerfileContent, makefileContent []byte) error {
	if dockerfileContent == nil && makefileContent == nil {
		log.Println("⚠️  No Dockerfile or Makefile content to parse, proceeding with defaults")
		return nil
	}

	parser.ParseOptionalFileInfo(item, dockerfileContent, makefileContent)

	item.BuildFiles.Spec = parser.ExtractStaticBuildValues(item.BuildFiles.Dockerfile)

	return nil
}

// buildSpec resolves build targets, transforms to a DALEC spec, and returns its bytes.
func buildSpec(item *workplan.WorkItem) ([]byte, []string, error) {
	dalecSpec := transformer.TransformToDalec(item)

	specBytes, err := parser.EncodeDalecSpec(dalecSpec)
	if err != nil {
		return nil, nil, fmt.Errorf("encoding output YAML: %w", err)
	}

	return specBytes, item.Naming.Targets, nil
}
