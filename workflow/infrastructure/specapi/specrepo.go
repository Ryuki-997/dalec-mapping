// ═══════════════════════════════════════════════════════════════════════════════
// SpecRepo — All read operations against the managed dalec spec repository.
//
//   Consolidates fetching of onboard files, spec files, and tree metadata from
//   the team's remote repository (aks-dalec-build-defs). All exported functions
//   share the "SpecRepo" prefix to indicate they operate on this single remote
//   target.
//
//   Functions:
//     SpecRepoFetchTree()                — full recursive git tree (path index lives in pathcache.Cache) → ([]interface{}, error)
//     SpecRepoBuildPathIndex(entries)    — builds path lookup set from tree entries                      → map[string]bool
//     SpecRepoFetchOnboard(onboardPath)  — fetches and decodes onboard.yml                               → ([]workplan.WorkGroup, error)
//     SpecRepoFetchFile(remotePath)      — fetches and base64-decodes any single file                    → ([]byte, error)
//     SpecRepoFetchSpec(remotePath)      — fetches and parses a spec YAML file                           → (*yaml.Node, error)
//     SpecRepoFetchCommit(specFilePath)  — reads args.COMMIT from an existing spec                       → (string, error)
//     FindMapValue(root, key)            — YAML node lookup helper                                       → *yaml.Node
// ═══════════════════════════════════════════════════════════════════════════════

package specapi

import (
	"encoding/base64"
	"fmt"
	"strings"

	"dalec-mapping/config"
	"dalec-mapping/domain/pathcache"
	"dalec-mapping/domain/workplan"
	"dalec-mapping/workflow/infrastructure/github"

	"gopkg.in/yaml.v3"
)

// OnboardAPIPath now lives in domain/pathcache/paths.go.

// SpecRepoFetchTree fetches the full recursive git tree from the spec repo.
// Uses config.OnboardOwner/config.OnboardRepo/config.OnboardBranch constants to target the remote.
// The path index is loaded into pathcache.Cache as a side effect; callers
// query existence via pathcache.Has.
// Returns:
//   - treeEntries: raw GitHub tree API entries ([]interface{})
//   - error: non-nil on API failure or unexpected response format
func SpecRepoFetchTree() ([]interface{}, error) {
	data, err := github.FetchJSON(pathcache.OnboardAPIPath("git/trees/%s?recursive=1", config.OnboardBranch))
	if err != nil {
		return nil, fmt.Errorf("failed to fetch onboard data: %w", err)
	}
	treeEntries, ok := data["tree"].([]interface{})
	if !ok {
		return nil, fmt.Errorf("unexpected response format: 'tree' field is missing or not an array")
	}

	pathcache.Set(SpecRepoBuildPathIndex(treeEntries))

	return treeEntries, nil
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

// SpecRepoFetchOnboard fetches a partner-level onboard.yml and decodes it
// into a list of WorkGroups. Each returned group has GroupName set
// (PRID empty) and Components containing one skeleton *WorkComponent per declared
// component (Name/DockerfileDir/MakefileDir only — Tag/Revision/Naming/
// Group are filled in by Phase 1 fan-out). Target validation happens
// inside workplan.Decode so the returned slice only contains groups whose
// targets list has at least one supported target.
//   - onboardPath: full path to onboard.yml in the spec repo (e.g. "specs/containernetworking/onboard.yml")
func SpecRepoFetchOnboard(onboardPath string) ([]workplan.WorkGroup, error) {
	contentsPath := pathcache.OnboardAPIPath("contents/%s?ref=%s", onboardPath, config.OnboardBranch)
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

	groups, err := workplan.Decode(rawContent)
	if err != nil {
		return nil, fmt.Errorf("failed to decode %s: %w", onboardPath, err)
	}

	if len(groups) == 0 {
		return nil, fmt.Errorf("no components found in %s", onboardPath)
	}
	return groups, nil
}

// SpecRepoFetchFile fetches a single file from the onboard repo at the given
// path and returns its raw decoded bytes. Used for plain-text artifacts
// (Dockerfile/Makefile snapshots under BuildFiles/) where YAML parsing is
// not appropriate.
func SpecRepoFetchFile(remotePath string) ([]byte, error) {
	fileData, err := github.FetchJSON(pathcache.OnboardAPIPath("contents/%s?ref=%s", remotePath, config.OnboardBranch))
	if err != nil {
		return nil, fmt.Errorf("failed to fetch %s: %w", remotePath, err)
	}

	contentStr, ok := fileData["content"].(string)
	if !ok {
		return nil, fmt.Errorf("unexpected response: missing content field for %s", remotePath)
	}

	decoded, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(contentStr, "\n", ""))
	if err != nil {
		return nil, fmt.Errorf("failed to decode content for %s: %w", remotePath, err)
	}
	return decoded, nil
}

// SpecRepoFetchSpec fetches and decodes a spec file from the onboard repo at the
// given path. Returns the parsed YAML document node.
//   - remotePath: repo-relative path to the specfile (e.g. "specs/foo/bar-1.0.0-1-specfile.yml")
func SpecRepoFetchSpec(remotePath string) (*yaml.Node, error) {
	specBytes, err := SpecRepoFetchFile(remotePath)
	if err != nil {
		return nil, err
	}

	var specNode yaml.Node
	if err := yaml.Unmarshal(specBytes, &specNode); err != nil {
		return nil, fmt.Errorf("failed to parse spec YAML for %s: %w", remotePath, err)
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
