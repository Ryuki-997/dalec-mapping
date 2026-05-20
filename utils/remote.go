// ═══════════════════════════════════════════════════════════════════════════════
// SpecRepo — All read operations against the managed dalec spec repository.
//
//   Consolidates fetching of onboard files, spec files, cached build files,
//   and tree metadata from the team's remote repository (aks-dalec-build-defs).
//   All exported functions share the "SpecRepo" prefix to indicate they operate
//   on this single remote target.
//
//   Functions:
//     SpecRepoFetchTree()                                              — full recursive git tree + path index                → ([]interface{}, map[string]bool, error)
//     SpecRepoBuildPathIndex(treeEntries)                              — builds path lookup set from tree entries             → map[string]bool
//     SpecRepoFetchOnboard(onboardPath, onboardDir, specRepository)    — fetches and decodes onboard.yml                     → ([]ComponentConfig, error)
//     SpecRepoFetchCachedBuildFiles(component)                         — fetches cached Dockerfile/Makefile siblings          → void (mutates component)
//     SpecRepoFetchSpec(remotePath)                                    — fetches and parses a spec YAML file                 → (*yaml.Node, error)
//     SpecRepoFetchCommit(specFilePath)                                — reads args.COMMIT from an existing spec             → (string, error)
//     SpecRepoFetchLatestRevision(specDir, specImage, tag, revision)   — fetches previous revision spec for same version     → (*yaml.Node, error)
//     SpecRepoFindLatestVersion(specDir, specImage, treePaths)         — finds latest spec across all versions               → (string, bool)
//     FindMapValue(root, key)                                          — YAML node lookup helper                             → *yaml.Node
// ═══════════════════════════════════════════════════════════════════════════════

package utils

import (
	"encoding/base64"
	"fmt"
	"log"
	"regexp"
	"strconv"
	"strings"

	"dalec-mapping/domain/onboarding"
	"dalec-mapping/infrastructure/github"

	"gopkg.in/yaml.v3"
)

// SpecRepoFetchTree fetches the full recursive git tree from the spec repo.
// Uses OnboardOwner/OnboardRepo/OnboardBranch constants to target the remote.
// Returns:
//   - treeEntries: raw GitHub tree API entries ([]interface{})
//   - existingPaths: O(1) lookup set of every file path in the repo
//   - error: non-nil on API failure or unexpected response format
func SpecRepoFetchTree() ([]interface{}, map[string]bool, error) {
	data, err := github.FetchJSON(fmt.Sprintf(
		"repos/%s/%s/git/trees/%s?recursive=1",
		OnboardOwner, OnboardRepo, OnboardBranch,
	))
	if err != nil {
		return nil, nil, fmt.Errorf("failed to fetch onboard data: %w", err)
	}
	treeEntries, ok := data["tree"].([]interface{})
	if !ok {
		return nil, nil, fmt.Errorf("unexpected response format: 'tree' field is missing or not an array")
	}

	existingPaths := SpecRepoBuildPathIndex(treeEntries)

	return treeEntries, existingPaths, nil
}

// SpecRepoBuildPathIndex builds a lookup set of all file paths in the repo tree.
//   - treeEntries: raw GitHub tree API entries from FetchSpecRepoTree
//
// Returns a map[path]bool for O(1) existence checks.
func SpecRepoBuildPathIndex(treeEntries []interface{}) map[string]bool {
	pathIndex := make(map[string]bool)
	for _, entry := range treeEntries {
		entryMap, ok := entry.(map[string]interface{})
		if !ok {
			continue
		}
		entryPath, ok := entryMap["path"].(string)
		if !ok {
			continue
		}
		pathIndex[entryPath] = true
	}
	return pathIndex
}

// SpecRepoFetchOnboard fetches a partner-level onboard.yml, unmarshals it into
// an OnboardFile, and flattens all components into a slice of ComponentConfig.
//   - onboardPath: full path to onboard.yml in the spec repo (e.g. "specs/containernetworking/onboard.yml")
//   - onboardDir: parent directory of the onboard.yml (e.g. "specs/containernetworking")
//   - specRepository: partner name used in specfile content (e.g. "containernetworking")
func SpecRepoFetchOnboard(onboardPath, onboardDir, specRepository string) ([]onboarding.ComponentConfig, error) {
	contentsPath := fmt.Sprintf("repos/%s/%s/contents/%s?ref=%s",
		OnboardOwner, OnboardRepo, onboardPath, OnboardBranch)
	data, err := github.FetchJSON(contentsPath)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch onboard file %s: %w", onboardPath, err)
	}

	encodedContent, ok := data["content"].(string)
	if !ok {
		return nil, fmt.Errorf("no content field in response for %s", onboardPath)
	}

	rawContent, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(encodedContent, "\n", ""))
	if err != nil {
		return nil, fmt.Errorf("failed to decode base64 content for %s: %w", onboardPath, err)
	}

	if len(rawContent) == 0 {
		return nil, fmt.Errorf("skipping empty onboard file: %s", onboardPath)
	}

	var onboardFile onboarding.OnboardFile
	if err := yaml.Unmarshal(rawContent, &onboardFile); err != nil {
		return nil, fmt.Errorf("failed to unmarshal %s: %w", onboardPath, err)
	}

	components := onboardFile.Flatten(onboardDir, specRepository)
	if len(components) == 0 {
		return nil, fmt.Errorf("no components found in %s", onboardPath)
	}

	for _, component := range components {
		if component.SpecRepository != "" {
			log.Printf("Onboard Data: %s/%s repo=%s tags=%v\n", component.SpecRepository, component.SpecImageName, component.Repository, component.TagPatterns)
		} else {
			log.Printf("Onboard Data: %s repo=%s tags=%v\n", component.SpecImageName, component.Repository, component.TagPatterns)
		}
	}
	return components, nil
}

// SpecRepoFetchCachedBuildFiles fetches the previously-committed Dockerfile/Makefile
// from the spec repo's onboard directory. These cached files are used to
// detect content changes during the diff step.
//   - component: mutated in place — DockerfileContent and MakefileContent are populated
func SpecRepoFetchCachedBuildFiles(component *onboarding.ComponentConfig) {
	rawBaseURL := fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/%s",
		OnboardOwner, OnboardRepo, OnboardBranch)

	dockerfilePath := component.OnboardDir + "/Dockerfile"
	dockerfileContent, err := github.FetchRawContent(rawBaseURL + "/" + dockerfilePath)
	if err == nil {
		component.DockerfileContent = dockerfileContent
	}

	makefilePath := component.OnboardDir + "/Makefile"
	makefileContent, err := github.FetchRawContent(rawBaseURL + "/" + makefilePath)
	if err == nil {
		component.MakefileContent = makefileContent
	}

	hasDockerfile := component.DockerfileContent != nil
	hasMakefile := component.MakefileContent != nil
	if !hasDockerfile && !hasMakefile {
		log.Printf("No sibling Dockerfile/Makefile found for %s — treating as first-time onboard\n", component.SpecImageName)
		return
	}
	log.Printf("Found existing siblings for %s (Dockerfile=%v, Makefile=%v) — will diff\n", component.SpecImageName, hasDockerfile, hasMakefile)
}

// SpecRepoFetchSpec fetches and decodes a spec file from the onboard repo at the
// given path. Returns the parsed YAML document node.
//   - remotePath: repo-relative path to the specfile (e.g. "specs/foo/bar-1.0.0-1-specfile.yml")
func SpecRepoFetchSpec(remotePath string) (*yaml.Node, error) {
	fileData, err := github.FetchJSON(fmt.Sprintf("repos/%s/%s/contents/%s?ref=%s",
		OnboardOwner, OnboardRepo, remotePath, OnboardBranch))
	if err != nil {
		return nil, fmt.Errorf("failed to fetch spec %s: %w", remotePath, err)
	}

	contentStr, ok := fileData["content"].(string)
	if !ok {
		return nil, fmt.Errorf("unexpected response: missing content field for %s", remotePath)
	}

	specBytes, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(contentStr, "\n", ""))
	if err != nil {
		return nil, fmt.Errorf("failed to decode spec content: %w", err)
	}

	var specNode yaml.Node
	if err := yaml.Unmarshal(specBytes, &specNode); err != nil {
		return nil, fmt.Errorf("failed to parse spec YAML: %w", err)
	}

	return &specNode, nil
}

// SpecRepoFetchCommit fetches the existing spec from the onboard repo and
// extracts the args.COMMIT value.
//   - specFilePath: repo-relative path to the specfile to read COMMIT from
//
// Returns the commit SHA string stored in the spec's args.COMMIT field.
func SpecRepoFetchCommit(specFilePath string) (string, error) {
	specNode, err := SpecRepoFetchSpec(specFilePath)
	if err != nil {
		return "", err
	}

	argsNode := FindMapValue(specNode, "args")
	if argsNode == nil {
		return "", fmt.Errorf("existing spec %s missing 'args' section", specFilePath)
	}

	commitNode := FindMapValue(argsNode, "COMMIT")
	if commitNode == nil {
		return "", fmt.Errorf("existing spec %s missing args.COMMIT", specFilePath)
	}

	return commitNode.Value, nil
}

// SpecRepoFetchLatestRevision fetches the previous revision's spec for the same
// version from the onboard repo. Accepts explicit parameters to avoid circular
// imports with the pipeline package.
//   - specDir: component's onboard directory (e.g. "specs/containernetworking/azure-cns")
//   - specImage: component image name (e.g. "azure-cns")
//   - tag: stripped semver tag with v prefix (e.g. "v1.8.5")
//   - currentRevision: the revision being created (fetches currentRevision-1)
func SpecRepoFetchLatestRevision(specDir, specImage, tag string, currentRevision int) (*yaml.Node, error) {
	previousRevision := currentRevision - 1
	version := strings.TrimPrefix(tag, "v")
	remotePath := fmt.Sprintf("%s/%s-%s-%d-specfile.yml", specDir, specImage, version, previousRevision)

	log.Printf("   Template (same version, R%d): %s\n", previousRevision, remotePath)
	return SpecRepoFetchSpec(remotePath)
}

// SpecRepoFindLatestVersion scans treePaths for the highest-version spec file
// belonging to the given component. Returns the full path and true if found,
// or ("", false) if no spec exists for this component.
//   - specDir: component's onboard directory (e.g. "specs/containernetworking/azure-cns")
//   - specImage: component image name (e.g. "azure-cns")
//   - treePaths: O(1) path set from FetchSpecRepoTree/BuildPathIndex
//
// Selects the spec with the highest semver, breaking ties by highest revision.
func SpecRepoFindLatestVersion(specDir, specImage string, treePaths map[string]bool) (string, bool) {
	pattern := regexp.MustCompile(
		fmt.Sprintf(`^%s/%s-(\d+\.\d+\.\d+)-(\d+)-specfile\.yml$`,
			regexp.QuoteMeta(specDir),
			regexp.QuoteMeta(specImage)),
	)

	var bestPath string
	var bestVersion []int
	bestRevision := 0
	found := false

	for filePath := range treePaths {
		matches := pattern.FindStringSubmatch(filePath)
		if matches == nil {
			continue
		}

		versionParts := strings.Split(matches[1], ".")
		if len(versionParts) != 3 {
			continue
		}

		version := make([]int, 3)
		valid := true
		for i, part := range versionParts {
			n, err := strconv.Atoi(part)
			if err != nil {
				valid = false
				break
			}
			version[i] = n
		}
		if !valid {
			continue
		}

		revision, err := strconv.Atoi(matches[2])
		if err != nil {
			continue
		}

		if !found {
			bestPath = filePath
			bestVersion = version
			bestRevision = revision
			found = true
			continue
		}

		versionComparison := compareVersions(version, bestVersion)
		if versionComparison > 0 || (versionComparison == 0 && revision > bestRevision) {
			bestPath = filePath
			bestVersion = version
			bestRevision = revision
		}
	}

	return bestPath, found
}

// compareVersions compares two [major, minor, patch] slices.
// Returns +1, 0, or -1.
func compareVersions(a, b []int) int {
	for i := 0; i < 3; i++ {
		if a[i] > b[i] {
			return 1
		}
		if a[i] < b[i] {
			return -1
		}
	}
	return 0
}

// FindMapValue searches a YAML node tree for a mapping key and returns its value node.
//   - root: any YAML node (DocumentNode, MappingNode, etc.)
//   - key: the mapping key to search for (e.g. "args", "COMMIT")
//
// Returns nil if the key is not found.
func FindMapValue(root *yaml.Node, key string) *yaml.Node {
	if root == nil {
		return nil
	}
	if root.Kind == yaml.DocumentNode {
		for _, child := range root.Content {
			if result := FindMapValue(child, key); result != nil {
				return result
			}
		}
		return nil
	}
	if root.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == key {
			return root.Content[i+1]
		}
	}
	return nil
}
