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
//     SpecRepoFetchOnboard(onboardPath)                                — fetches and decodes onboard.yml                     → (OnboardFile, error)
//     SpecRepoExtractTemplateVersion(templatePath, n)                  — parses version segment from a spec file path        → (string, error)
//     SpecRepoFetchSpec(remotePath)                                    — fetches and parses a spec YAML file                 → (*yaml.Node, error)
//     SpecRepoFetchCommit(specFilePath)                                — reads args.COMMIT from an existing spec             → (string, error)
//     SpecRepoFetchLatestRevision(n, tag, existingPaths)               — fetches latest revision spec for same version     → (*yaml.Node, error)
//     SpecRepoFindLatestMinorVersion(n, targetTag, treePaths)          — finds latest spec sharing target's major.minor → (string, bool)
//     FindMapValue(root, key)                                          — YAML node lookup helper                             → *yaml.Node
// ═══════════════════════════════════════════════════════════════════════════════

package specapi

import (
	"encoding/base64"
	"fmt"
	"log"
	"regexp"
	"strconv"
	"strings"

	"dalec-mapping/config"
	"dalec-mapping/domain/naming"
	"dalec-mapping/domain/onboarding"
	"dalec-mapping/workflow/infrastructure/github"

	"gopkg.in/yaml.v3"
)

// SpecRepoFetchTree fetches the full recursive git tree from the spec repo.
// Uses config.OnboardOwner/config.OnboardRepo/config.OnboardBranch constants to target the remote.
// Returns:
//   - treeEntries: raw GitHub tree API entries ([]interface{})
//   - existingPaths: O(1) lookup set of every file path in the repo
//   - error: non-nil on API failure or unexpected response format
func SpecRepoFetchTree() ([]interface{}, map[string]bool, error) {
	data, err := github.FetchJSON(fmt.Sprintf(
		"repos/%s/%s/git/trees/%s?recursive=1",
		config.OnboardOwner, config.OnboardRepo, config.OnboardBranch,
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

// SpecRepoFetchOnboard fetches a partner-level onboard.yml and unmarshals it
// into an OnboardFile. Target validation happens inside UnmarshalYAML so the
// returned file only contains components with at least one supported target.
//   - onboardPath: full path to onboard.yml in the spec repo (e.g. "specs/containernetworking/onboard.yml")
func SpecRepoFetchOnboard(onboardPath string) (onboarding.OnboardFile, error) {
	var onboardFile onboarding.OnboardFile

	contentsPath := fmt.Sprintf("repos/%s/%s/contents/%s?ref=%s",
		config.OnboardOwner, config.OnboardRepo, onboardPath, config.OnboardBranch)
	data, err := github.FetchJSON(contentsPath)
	if err != nil {
		return onboardFile, fmt.Errorf("failed to fetch onboard file %s: %w", onboardPath, err)
	}

	encodedContent, ok := data["content"].(string)
	if !ok {
		return onboardFile, fmt.Errorf("no content field in response for %s", onboardPath)
	}

	rawContent, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(encodedContent, "\n", ""))
	if err != nil {
		return onboardFile, fmt.Errorf("failed to decode base64 content for %s: %w", onboardPath, err)
	}

	if len(rawContent) == 0 {
		return onboardFile, fmt.Errorf("skipping empty onboard file: %s", onboardPath)
	}

	if err := yaml.Unmarshal(rawContent, &onboardFile); err != nil {
		return onboardFile, fmt.Errorf("failed to unmarshal %s: %w", onboardPath, err)
	}

	if len(onboardFile.Standalone) == 0 && len(onboardFile.Groups) == 0 {
		return onboardFile, fmt.Errorf("no components found in %s", onboardPath)
	}
	return onboardFile, nil
}

// SpecRepoExtractTemplateVersion parses the stripped version (no "v") out of
// a spec file path produced by SpecRepoFindLatestMinorVersion. Expected shape:
//
//	<OnboardDir>/<SpecImageName>-<X.Y.Z>-<R>-specfile.yml
func SpecRepoExtractTemplateVersion(templatePath string, component naming.Naming) (string, error) {
	pattern := regexp.MustCompile(
		fmt.Sprintf(`^%s/%s-(\d+\.\d+\.\d+)-\d+-specfile\.yml$`,
			regexp.QuoteMeta(component.OnboardDir),
			regexp.QuoteMeta(component.SpecImageName)),
	)
	matches := pattern.FindStringSubmatch(templatePath)
	if matches == nil {
		return "", fmt.Errorf("template path %q does not match expected spec filename pattern", templatePath)
	}
	return matches[1], nil
}

// SpecRepoFetchSpec fetches and decodes a spec file from the onboard repo at the
// given path. Returns the parsed YAML document node.
//   - remotePath: repo-relative path to the specfile (e.g. "specs/foo/bar-1.0.0-1-specfile.yml")
func SpecRepoFetchSpec(remotePath string) (*yaml.Node, error) {
	fileData, err := github.FetchJSON(fmt.Sprintf("repos/%s/%s/contents/%s?ref=%s",
		config.OnboardOwner, config.OnboardRepo, remotePath, config.OnboardBranch))
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

// SpecRepoFetchLatestRevision finds the highest existing revision for the same
// version by scanning existingPaths, then fetches that spec from the remote repo.
//   - n: component identity (uses n.OnboardDir, n.SpecImageName)
//   - tag: stripped semver tag, with or without "v" prefix (e.g. "v1.8.5" or "1.8.5");
//     the prefix is trimmed internally to match the remote storage convention.
//   - existingPaths: O(1) path set from FetchSpecRepoTree/BuildPathIndex
func SpecRepoFetchLatestRevision(n naming.Naming, tag string, existingPaths map[string]bool) (*yaml.Node, error) {
	version := strings.TrimPrefix(tag, "v")
	pattern := regexp.MustCompile(
		fmt.Sprintf(`^%s/%s-%s-(\d+)-specfile\.yml$`,
			regexp.QuoteMeta(n.OnboardDir),
			regexp.QuoteMeta(n.SpecImageName),
			regexp.QuoteMeta(version)),
	)

	highestRevision := 0
	found := false
	for path := range existingPaths {
		matches := pattern.FindStringSubmatch(path)
		if matches == nil {
			continue
		}
		revisionNumber, err := strconv.Atoi(matches[1])
		if err != nil {
			continue
		}
		found = true
		if revisionNumber > highestRevision {
			highestRevision = revisionNumber
		}
	}

	if !found {
		return nil, fmt.Errorf("no existing revision found for %s/%s-%s-*-specfile.yml", n.OnboardDir, n.SpecImageName, version)
	}

	remotePath := fmt.Sprintf("%s/%s-%s-%d-specfile.yml", n.OnboardDir, n.SpecImageName, version, highestRevision)
	log.Printf("   Template (same version, R%d): %s\n", highestRevision, remotePath)
	return SpecRepoFetchSpec(remotePath)
}

// SpecRepoFindLatestMinorVersion scans treePaths for the best template spec
// to bump from when producing a new spec for targetTag. The search proceeds in
// two stages, both restricted to the same major version as targetTag:
//
//  1. Preferred: the same major.minor as targetTag. Among matches, highest
//     patch wins; ties broken by highest revision.
//  2. Fallback: if no spec exists for the target's minor, scan lower minors
//     within the same major (e.g. target 1.7 → 1.6, 1.5, …). The highest
//     minor with any matching spec is chosen; within that minor, highest
//     patch+revision wins.
//
// Returns ("", false) only when the component has no specs in the same major
// at all. Callers rely on the build-file diff (contentChanged) to decide
// whether a returned template is actually safe to reuse — if Dockerfile or
// Makefile changed between the template's tag and targetTag, the orchestrator
// falls back to GENERATE instead of BUMP VERSION.
//
//   - n: component identity (uses n.OnboardDir, n.SpecImageName)
//   - targetTag: the iteration's tag string (e.g. "v1.7.2" or "1.7.2") whose
//     major.minor selects the eligible spec family
//   - treePaths: O(1) path set from FetchSpecRepoTree/BuildPathIndex
func SpecRepoFindLatestMinorVersion(n naming.Naming, targetTag string, treePaths map[string]bool) (string, bool) {
	targetMajor, targetMinor, ok := parseMajorMinor(targetTag)
	if !ok {
		return "", false
	}

	candidates := collectSameMajorCandidates(n.OnboardDir, n.SpecImageName, targetMajor, treePaths)
	if len(candidates) == 0 {
		return "", false
	}

	if path, found := pickBestInMinor(candidates, targetMinor); found {
		return path, true
	}

	return pickBestLowerMinor(candidates, targetMinor)
}

// specCandidate describes one parsed spec file path eligible for template selection.
type specCandidate struct {
	path     string
	version  []int // [major, minor, patch]
	revision int
}

// collectSameMajorCandidates returns every spec for (specDir, specImage) whose
// major version equals targetMajor. Paths that do not match the canonical
// "<dir>/<image>-X.Y.Z-R-specfile.yml" shape are silently skipped. Remote spec
// files are stored without a leading "v" on the version component.
func collectSameMajorCandidates(specDir, specImage string, targetMajor int, treePaths map[string]bool) []specCandidate {
	pattern := regexp.MustCompile(
		fmt.Sprintf(`^%s/%s-(\d+\.\d+\.\d+)-(\d+)-specfile\.yml$`,
			regexp.QuoteMeta(specDir),
			regexp.QuoteMeta(specImage)),
	)

	candidates := make([]specCandidate, 0)
	for filePath := range treePaths {
		matches := pattern.FindStringSubmatch(filePath)
		if matches == nil {
			continue
		}

		version, ok := parseSemverParts(matches[1])
		if !ok {
			continue
		}
		if version[0] != targetMajor {
			continue
		}

		revision, err := strconv.Atoi(matches[2])
		if err != nil {
			continue
		}

		candidates = append(candidates, specCandidate{
			path:     filePath,
			version:  version,
			revision: revision,
		})
	}
	return candidates
}

// pickBestInMinor returns the highest patch+revision spec whose minor equals
// targetMinor, or ("", false) when no candidate has that exact minor.
func pickBestInMinor(candidates []specCandidate, targetMinor int) (string, bool) {
	bestIndex := -1
	for index, candidate := range candidates {
		if candidate.version[1] != targetMinor {
			continue
		}
		if bestIndex == -1 || isHigherPatchRevision(candidate, candidates[bestIndex]) {
			bestIndex = index
		}
	}
	if bestIndex == -1 {
		return "", false
	}
	return candidates[bestIndex].path, true
}

// pickBestLowerMinor selects the highest minor below targetMinor, then within
// that minor returns the highest patch+revision spec. Returns ("", false) when
// no candidate has a minor strictly less than targetMinor.
func pickBestLowerMinor(candidates []specCandidate, targetMinor int) (string, bool) {
	highestLowerMinor := -1
	for _, candidate := range candidates {
		minor := candidate.version[1]
		if minor >= targetMinor {
			continue
		}
		if minor > highestLowerMinor {
			highestLowerMinor = minor
		}
	}
	if highestLowerMinor == -1 {
		return "", false
	}

	path, found := pickBestInMinor(candidates, highestLowerMinor)
	if !found {
		return "", false
	}
	log.Printf("   Minor fallback: no spec at minor %d, using highest lower minor %d → %s\n", targetMinor, highestLowerMinor, path)
	return path, true
}

// isHigherPatchRevision reports whether candidate ranks above current using
// (patch, revision) lexicographic comparison. Major and minor are assumed equal.
func isHigherPatchRevision(candidate, current specCandidate) bool {
	comparison := compareVersions(candidate.version, current.version)
	if comparison != 0 {
		return comparison > 0
	}
	return candidate.revision > current.revision
}

// parseSemverParts splits a "X.Y.Z" string into a 3-int slice. Returns ok=false
// when the input has the wrong shape or any segment is not a valid integer.
func parseSemverParts(version string) ([]int, bool) {
	parts := strings.Split(version, ".")
	if len(parts) != 3 {
		return nil, false
	}
	parsed := make([]int, 3)
	for index, part := range parts {
		number, err := strconv.Atoi(part)
		if err != nil {
			return nil, false
		}
		parsed[index] = number
	}
	return parsed, true
}

// parseMajorMinor extracts the major and minor components from a semver-like
// tag string. Accepts forms "v1.7.2", "1.7.2", "v1.7", or "1.7". Returns
// ok=false when the input does not contain at least major.minor digits.
func parseMajorMinor(tag string) (int, int, bool) {
	trimmed := strings.TrimPrefix(tag, "v")
	parts := strings.Split(trimmed, ".")
	if len(parts) < 2 {
		return 0, 0, false
	}
	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, false
	}
	minor, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0, false
	}
	return major, minor, true
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
