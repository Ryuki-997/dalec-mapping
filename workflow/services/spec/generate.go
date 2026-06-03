// ═══════════════════════════════════════════════════════════════════════════════
// Spec — Generate
//
//   Parses the Dockerfile and Makefile, then transforms them into a DALEC spec
//   YAML file written to the result directory.
//
//   Functions are ordered by call sequence:
//     GenerateSpec()
//       → parseAndExtract()
//       → buildRepoInfo()
//       → buildSpec()
// ═══════════════════════════════════════════════════════════════════════════════

package spec

import (
	"fmt"
	"log"

	"dalec-mapping/domain/repository"
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
	cfg := item.Component

	if err := parseAndExtract(item, item.BuildFiles.Dockerfile.Source, item.BuildFiles.Makefile.Source); err != nil {
		return nil, nil, fmt.Errorf("parsing build files: %w", err)
	}

	repoInfo, err := buildRepoInfo(item, cfg.Repository)
	if err != nil {
		return nil, nil, fmt.Errorf("fetching repository info: %w", err)
	}
	item.BuildFiles.RepoInfo = repoInfo

	specBytes, resolvedTargets, err := buildSpec(item)
	if err != nil {
		return nil, nil, err
	}

	log.Printf("Output: targets=%v\n", resolvedTargets)
	return specBytes, resolvedTargets, nil
}

// buildRepoInfo gathers every piece of upstream repository metadata for the
// current WorkItem and returns a fully-formed repository.RepoInfo. The caller
// assigns the result to item.BuildFiles.RepoInfo exactly once; no field is
// mutated afterwards.
func buildRepoInfo(item *workplan.WorkItem, repoURL string) (repository.RepoInfo, error) {
	base, fetchedLicense, err := fetchBaseRepoInfo(repoURL)
	if err != nil {
		return repository.RepoInfo{}, err
	}

	item.Component.License = resolveLicense(item.Component.License, fetchedLicense)

	tagSet := item.Tag
	commitSHA, err := tagcache.Lookup(repoURL, tagSet.Full)
	if err != nil {
		return repository.RepoInfo{}, fmt.Errorf("failed to resolve commit for tag %s: %w", tagSet.Full, err)
	}

	base.LatestCommit = commitSHA
	base.Version = tagSet.Version
	base.GoVersion = detectGoVersion(item)

	if ado.IsADORepo(repoURL) {
		base.Generator = ado.DetectADOGenerator(repoURL, base.ComponentPath, tagSet.Full)
	}

	return base, nil
}

// resolveLicense applies the priority: configured → fetched → "proprietary".
func resolveLicense(configured, fetched string) string {
	if configured != "" {
		return configured
	}
	if fetched != "" {
		return fetched
	}
	return "proprietary"
}

// fetchBaseRepoInfo dispatches to the GitHub or ADO fetcher and returns the
// remote-side fields as a value (dereferenced from the upstream pointer) plus
// the fetched license ("" when none).
func fetchBaseRepoInfo(repoURL string) (repository.RepoInfo, string, error) {
	if ado.IsADORepo(repoURL) {
		info, fetchedLicense, err := ado.FetchADORepoInfo(repoURL)
		if err != nil {
			return repository.RepoInfo{}, "", err
		}
		return *info, fetchedLicense, nil
	}
	info, fetchedLicense, err := github.FetchRepoInfo(repoURL)
	if err != nil {
		return repository.RepoInfo{}, "", err
	}
	return *info, fetchedLicense, nil
}

// detectGoVersion returns the Go toolchain version pinned in the Dockerfile
// stages, or "" when no Go base image is detected.
func detectGoVersion(item *workplan.WorkItem) string {
	pin := parser.DetectGoToolchainPin(item.BuildFiles.Dockerfile.Stages)
	if pin == nil {
		return ""
	}
	return pin.GoVersion()
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

	return specBytes, item.Component.Targets, nil
}
