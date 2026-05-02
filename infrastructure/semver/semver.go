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
//   Chunk 2 · TAG FILTERING  SpecFilePath(), FindLatestRevision()
//   Chunk 3 · PATTERN MATCH  matchPatternsFromMap(), matchRegexFromMap(), matchAllFromMap(), matchLargestFromMap()
//   Chunk 4 · SEMVER PARSE   ToTag(), parseSemver(), compareVersions()
// ═══════════════════════════════════════════════════════════════════════════════

package semver

import (
	"fmt"
	"log"
	"regexp"
	"strconv"

	"dalec-mapping/infrastructure/repository"
)

// semverRegex matches the first vX.Y.Z occurrence inside any tag string.
var semverRegex = regexp.MustCompile(`v(\d+)\.(\d+)\.(\d+)`)

// ─── Chunk 1 · TAG RESOLVING ────────────────────────────────────────────────

// FetchRepoTags fetches all tags from the repository and returns them as a
// map[tagName]commitHash for O(1) existence check and hash lookup.
// Tags are fetched once per repo and the map is reused for all components
// sharing the same repository URL.
func FetchRepoTags(repoURL string) (map[string]string, error) {
	var tags []repository.TagInfo
	var err error

	if repository.IsADORepo(repoURL) {
		tags, err = repository.FetchAllADOTags(repoURL)
	} else {
		owner, repoName, _ := repository.FetchRepositorySegments(repoURL)
		tags, err = repository.FetchAllGithubTags(owner, repoName)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to fetch tags for %s: %w", repoURL, err)
	}

	tagMap := make(map[string]string, len(tags))
	for _, tagInfo := range tags {
		tagMap[tagInfo.Name] = tagInfo.Commit
	}
	return tagMap, nil
}

// MatchTagSets matches regex patterns against pre-fetched tags and determines
// the next revision for each match. Returns actionable tags ready to become
// TagSet entries. The caller constructs TagSets from the returned data.
func MatchTagSets(tagsByName map[string]string, regexPatterns []string, specDir, specImage string, treePaths map[string]bool) []ActionableTag {
	matchedNames := matchPatternsFromMap(tagsByName, regexPatterns)
	if len(matchedNames) == 0 {
		return nil
	}

	var actionable []ActionableTag
	for _, tagName := range matchedNames {
		commitHash := tagsByName[tagName]
		strippedTag := ToTag(tagName)
		latestRevision, found := FindLatestRevision(specDir, specImage, strippedTag, treePaths)

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

// SpecFilePath returns the remote path for a spec file at the given revision.
// Format: {specDir}/{specImage}-{tag}-R{revision}-specfile.yml
func SpecFilePath(specDir, specImage, tag string, revision int) string {
	return fmt.Sprintf("%s/%s-%s-R%d-specfile.yml", specDir, specImage, tag, revision)
}

// FindLatestRevision scans the tree paths for the highest revision number
// of a spec file matching {specImage}-{tag}-R{n}-specfile.yml.
// Returns (0, false) when no matching revision exists.
func FindLatestRevision(specDir, specImage, tag string, treePaths map[string]bool) (int, bool) {
	pattern := regexp.MustCompile(
		fmt.Sprintf(`^%s/%s-%s-R(\d+)-specfile\.yml$`,
			regexp.QuoteMeta(specDir),
			regexp.QuoteMeta(specImage),
			regexp.QuoteMeta(tag)),
	)

	highest := 0
	found := false
	for path := range treePaths {
		matches := pattern.FindStringSubmatch(path)
		if matches == nil {
			continue
		}
		n, err := strconv.Atoi(matches[1])
		if err != nil {
			continue
		}
		found = true
		if n > highest {
			highest = n
		}
	}
	return highest, found
}

// ─── Chunk 3 · PATTERN MATCH ────────────────────────────────────────────────

// matchPatternsFromMap resolves multiple regexTag patterns against a tag map,
// deduplicating results. Returns matched tag names in deterministic order.
func matchPatternsFromMap(tagsByName map[string]string, regexPatterns []string) []string {
	seen := make(map[string]bool)
	var result []string
	for _, pattern := range regexPatterns {
		for _, name := range matchRegexFromMap(tagsByName, pattern) {
			if !seen[name] {
				seen[name] = true
				result = append(result, name)
			}
		}
	}
	return result
}

// matchRegexFromMap resolves a single regexTag against a tag map:
//   - "latest": the single largest semver tag overall
//   - direct (e.g. v1.6.2): the tag itself if it exists
//   - regex  (e.g. v1\.6\.\d-main-\d+): all matching tags
func matchRegexFromMap(tagsByName map[string]string, pattern string) []string {
	if pattern == "latest" {
		re := regexp.MustCompile(`^v\d+\.\d+\.\d+$`)
		if name := matchLargestFromMap(tagsByName, re); name != "" {
			return []string{name}
		}
		return nil
	}

	re, err := regexp.Compile("^" + pattern + "$")
	if err != nil {
		log.Printf("⚠️  Invalid regex pattern %q, skipping: %v", pattern, err)
		return nil
	}
	return matchAllFromMap(tagsByName, re)
}

// matchAllFromMap returns all tag names from the map that match the regex.
// Checks the original name first, then falls back to the stripped tag.
func matchAllFromMap(tagsByName map[string]string, re *regexp.Regexp) []string {
	var matches []string
	for name := range tagsByName {
		if re.MatchString(name) || re.MatchString(ToTag(name)) {
			matches = append(matches, name)
		}
	}
	return matches
}

// matchLargestFromMap returns the name of the largest semver tag matching the regex.
func matchLargestFromMap(tagsByName map[string]string, re *regexp.Regexp) string {
	var largestName string
	var largestNums []int

	for name := range tagsByName {
		if !re.MatchString(name) && !re.MatchString(ToTag(name)) {
			continue
		}
		nums := parseSemver(name)
		if nums == nil {
			continue
		}
		if largestNums == nil || compareVersions(nums, largestNums) > 0 {
			largestName = name
			largestNums = nums
		}
	}
	return largestName
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
