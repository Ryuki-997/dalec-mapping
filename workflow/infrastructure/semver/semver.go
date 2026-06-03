// ═══════════════════════════════════════════════════════════════════════════════
// Semver — Tag resolution, filtering, pattern matching, and parsing.
//
//   Naming convention:
//     fullTag  = "azure-ipam/v0.4.0"  (prefixed tag as stored in the repo)
//     tag      = "v0.4.0"             (semver with leading "v")
//     shortTag = "0.4.0"              (semver without leading "v")
//     regexTag = "v0\.4\.\d+"         (onboard pattern that matches a family)
//
//   Chunk 1 · TAG RESOLVING  FetchRepoTags(), MatchTagSets()
//   Chunk 2 · TAG FILTERING  FindLatestRevision()
//   Chunk 3 · PATTERN MATCH  ResolveTagPatterns(), matchTags()
//   Chunk 4 · SEMVER PARSE   ToTag(), parseSemver()
// ═══════════════════════════════════════════════════════════════════════════════

package semver

import (
	"fmt"
	"log"
	"regexp"
	"strconv"
	"strings"

	"dalec-mapping/domain/naming"
	"dalec-mapping/workflow/infrastructure/ado"
	"dalec-mapping/workflow/infrastructure/github"
)

// semverRegex matches the first vX.Y.Z occurrence inside any tag string.
var semverRegex = regexp.MustCompile(`v(\d+)\.(\d+)\.(\d+)`)

// ─── Chunk 1 · TAG RESOLVING ────────────────────────────────────────────────

// FetchRepoTags fetches all tags from the repository and returns them as a
// map[tagName]commitHash for O(1) existence check and hash lookup.
// Tags are fetched once per repo and the map is reused for all components
// sharing the same repository URL.
func FetchRepoTags(repoURL string) (map[string]string, error) {
	if ado.IsADORepo(repoURL) {
		return ado.FetchAllADOTags(repoURL)
	}

	return github.FetchAllGithubTags(repoURL)
}

// MatchTagSets takes a pre-resolved set of tag names (already filtered by
// include/exclude) and determines the next revision for each. Returns
// actionable tags ready to become TagSet entries.
func MatchTagSets(tagsByName map[string]string, resolvedTagNames []string, n naming.Naming, treePaths map[string]bool) []ActionableTag {
	if len(resolvedTagNames) == 0 {
		return nil
	}

	var actionable []ActionableTag
	for _, tagName := range resolvedTagNames {
		commitHash, exists := tagsByName[tagName]
		if !exists {
			log.Printf("⚠️  Resolved tag %q not found in tags cache, skipping\n", tagName)
			continue
		}
		strippedTag := ToTag(tagName)
		latestRevision, found := FindLatestRevision(n, strippedTag, treePaths)

		nextRevision := 1
		if found {
			nextRevision = latestRevision + 1
		}

		actionable = append(actionable, ActionableTag{
			Name:         tagName,
			Commit:       commitHash,
			NextRevision: nextRevision,
		})
	}
	return actionable
}

// ─── Chunk 2 · TAG FILTERING ────────────────────────────────────────────────

// ActionableTag represents a tag that needs processing, with its next revision number.
type ActionableTag struct {
	Name         string // Full tag name (e.g. "azure-ipam/v0.4.0")
	Commit       string // Commit SHA the tag points to
	NextRevision int    // The revision number to create (e.g. 1 for new, 2 for second revision)
}

// FindLatestRevision scans the tree paths for the highest revision number
// of a spec file matching {n.SpecImageName}-{version}-{n}-specfile.yml.
// version may be supplied with or without a leading "v" — it is stripped internally.
// Returns (0, false) when no matching revision exists.
func FindLatestRevision(n naming.Naming, version string, treePaths map[string]bool) (int, bool) {
	version = strings.TrimPrefix(version, "v")
	pattern := n.SpecFilePathRegex(regexp.QuoteMeta(version), `(\d+)`)

	highest := 0
	found := false
	for path := range treePaths {
		matches := pattern.FindStringSubmatch(path)
		if matches == nil {
			continue
		}
		revisionNumber, err := strconv.Atoi(matches[1])
		if err != nil {
			continue
		}
		found = true
		if revisionNumber > highest {
			highest = revisionNumber
		}
	}
	return highest, found
}

// ─── Chunk 3 · PATTERN MATCH ────────────────────────────────────────────────

// ResolveTagPatterns resolves include and exclude patterns against the fetched
// tag map. Returns the included tags minus any that match an exclude pattern.
func ResolveTagPatterns(tagsByName map[string]string, includePatterns, excludePatterns []string) []string {
	includedTags := matchTags(tagsByName, includePatterns)
	if len(includedTags) == 0 {
		return nil
	}

	excludedTags := matchTags(tagsByName, excludePatterns)
	if len(excludedTags) == 0 {
		return includedTags
	}

	excludeSet := make(map[string]bool, len(excludedTags))
	for _, tag := range excludedTags {
		excludeSet[tag] = true
	}

	var result []string
	for _, tag := range includedTags {
		if excludeSet[tag] {
			log.Printf("Excluded tag %q\n", tag)
			continue
		}
		result = append(result, tag)
	}
	return result
}

// matchTags resolves regex patterns against the tag map by compiling them into
// a single alternation regex and returning all matching tag names.
func matchTags(tagsByName map[string]string, patterns []string) []string {
	if len(patterns) == 0 {
		return nil
	}

	var validPatterns []string
	for _, pattern := range patterns {
		if _, err := regexp.Compile(pattern); err != nil {
			log.Printf("⚠️  Invalid regex pattern %q, skipping: %v", pattern, err)
			continue
		}
		validPatterns = append(validPatterns, pattern)
	}
	if len(validPatterns) == 0 {
		return nil
	}

	combined, err := regexp.Compile("^(?:" + strings.Join(validPatterns, "|") + ")$")
	if err != nil {
		return nil
	}

	var matched []string
	for name := range tagsByName {
		if combined.MatchString(name) {
			matched = append(matched, name)
		}
	}
	return matched
}

// ─── Chunk 4 · SEMVER PARSE ─────────────────────────────────────────────────

// ToTag extracts the tag ("v{major}.{minor}.{patch}") from a fullTag,
// discarding any prefix or suffix (e.g. "azure-ipam/v0.4.0" → "v0.4.0").
// Returns the original string unchanged if no semver is found.
func ToTag(fullTag string) string {
	nums := parseSemver(fullTag)
	if nums == nil {
		return fullTag
	}
	return fmt.Sprintf("v%d.%d.%d", nums[0], nums[1], nums[2])
}

// parseSemver finds and parses the first vX.Y.Z occurrence in a fullTag.
func parseSemver(fullTag string) []int {
	m := semverRegex.FindStringSubmatch(fullTag)
	if m == nil {
		return nil
	}
	nums := make([]int, 3)
	for i, s := range m[1:4] {
		n, err := strconv.Atoi(s)
		if err != nil {
			return nil
		}
		nums[i] = n
	}
	return nums
}
