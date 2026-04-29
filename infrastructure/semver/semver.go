// ═══════════════════════════════════════════════════════════════════════════════
// Semver — Tag resolution, filtering, pattern matching, and parsing.
//
//   Naming convention:
//     fullTag  = "azure-ipam/v0.4.0"  (prefixed tag as stored in the repo)
//     tag      = "v0.4.0"             (semver with leading "v")
//     shortTag = "0.4.0"              (semver without leading "v")
//     regexTag = "v0\.4\.\d+"         (onboard pattern that matches a family)
//
//   Chunk 1 · TAG RESOLVING  ResolveRepoTags(), resolveADOTags(), resolveGithubTags()
//   Chunk 2 · TAG FILTERING  FilterNewTags(), FilterExistingTags(), SpecFilePath()
//   Chunk 3 · PATTERN MATCH  matchPatterns(), matchRegex(), matchAll(), matchLargest()
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

// ResolveRepoTags fetches tags (with commit SHAs) from the appropriate source
// and resolves regexTag patterns against them.
func ResolveRepoTags(repoURL string, regexTags []string) ([]repository.TagInfo, error) {
	if repository.IsADORepo(repoURL) {
		return resolveADOTags(repoURL, regexTags)
	}
	return resolveGithubTags(repoURL, regexTags)
}

// resolveADOTags fetches all ADO tags (all are treated as release tags) and
// matches the given patterns against them.
func resolveADOTags(repoURL string, regexTags []string) ([]repository.TagInfo, error) {
	tags, err := repository.FetchAllADOTags(repoURL)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch tags for %s: %w", repoURL, err)
	}
	return matchPatterns(tags, regexTags), nil
}

// resolveGithubTags fetches release-filtered and all git tags from GitHub,
// matches patterns against release tags, and warns about git-only matches.
func resolveGithubTags(repoURL string, regexTags []string) ([]repository.TagInfo, error) {
	owner, repoName, _ := repository.FetchRepositorySegments(repoURL)
	releaseTags, allTags, err := repository.FetchAllGithubTags(owner, repoName)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch tags for %s: %w", repoURL, err)
	}

	matched := matchPatterns(releaseTags, regexTags)

	// Warn about tags that exist as git tags but have no associated release
	matchedNames := make(map[string]bool, len(matched))
	for _, t := range matched {
		matchedNames[t.Name] = true
	}
	for _, pattern := range regexTags {
		for _, t := range matchRegex(allTags, pattern) {
			if !matchedNames[t.Name] {
				fmt.Printf("⏭  Skipping %s @ %s (stripped: %s): tag exists but has no associated release\n", repoURL, t.Name, ToTag(t.Name))
			}
		}
	}

	return matched, nil
}

// ─── Chunk 2 · TAG FILTERING ────────────────────────────────────────────────

// FilterNewTags returns tags whose spec files do NOT yet exist remotely.
func FilterNewTags(tags []repository.TagInfo, specDir, specImage string, existingPaths map[string]bool) []repository.TagInfo {
	var filtered []repository.TagInfo
	for _, t := range tags {
		sp := SpecFilePath(specDir, specImage, ToTag(t.Name))
		if existingPaths[sp] {
			log.Printf("⏭  Skipping %s @ %s: spec file already exists at %s\n", specImage, t.Name, sp)
			continue
		}
		filtered = append(filtered, t)
	}
	return filtered
}

// FilterExistingTags is the inverse of FilterNewTags — returns tags that already have specs.
func FilterExistingTags(tags []repository.TagInfo, specDir, specImage string, existingPaths map[string]bool) []repository.TagInfo {
	var existing []repository.TagInfo
	for _, t := range tags {
		sp := SpecFilePath(specDir, specImage, ToTag(t.Name))
		if existingPaths[sp] {
			existing = append(existing, t)
		}
	}
	return existing
}

// SpecFilePath returns the remote path for a spec file.
func SpecFilePath(specDir, specImage, tag string) string {
	return fmt.Sprintf("%s/%s-%s-specfile.yml", specDir, specImage, tag)
}

// TagNames extracts the Name field from a slice of TagInfo.
func TagNames(tags []repository.TagInfo) []string {
	names := make([]string, len(tags))
	for i, t := range tags {
		names[i] = t.Name
	}
	return names
}

// ─── Chunk 3 · PATTERN MATCH ────────────────────────────────────────────────

// matchPatterns resolves multiple regexTag patterns against a tag list, deduplicating results.
func matchPatterns(tags []repository.TagInfo, regexTags []string) []repository.TagInfo {
	seen := make(map[string]bool)
	var result []repository.TagInfo
	for _, pattern := range regexTags {
		for _, t := range matchRegex(tags, pattern) {
			if !seen[t.Name] {
				seen[t.Name] = true
				result = append(result, t)
			}
		}
	}
	return result
}

// matchRegex resolves a single regexTag against a list of tags:
//   - "latest": the single largest semver tag overall
//   - direct (e.g. v1.6.2): the tag itself if it exists
//   - regex  (e.g. v1\.6\.\d-main-\d+): all matching tags
func matchRegex(tags []repository.TagInfo, pattern string) []repository.TagInfo {
	if pattern == "latest" {
		re := regexp.MustCompile(`^v\d+\.\d+\.\d+$`)
		if t := matchLargest(tags, re); t != nil {
			return []repository.TagInfo{*t}
		}
		return nil
	}

	re, err := regexp.Compile("^" + pattern + "$")
	if err != nil {
		log.Printf("⚠️  Invalid regex pattern %q, skipping: %v", pattern, err)
		return nil
	}
	return matchAll(tags, re)
}

// matchAll returns all tags that match the regex.
// Checks the original name first, then falls back to the stripped tag
// (to support plain "v0\.4\.\d+" patterns against prefixed names like "azure-ipam/v0.4.0").
func matchAll(tags []repository.TagInfo, re *regexp.Regexp) []repository.TagInfo {
	var matches []repository.TagInfo
	for _, t := range tags {
		if re.MatchString(t.Name) || re.MatchString(ToTag(t.Name)) {
			matches = append(matches, t)
		}
	}
	return matches
}

// matchLargest returns the largest tag that matches the regex.
func matchLargest(tags []repository.TagInfo, re *regexp.Regexp) *repository.TagInfo {
	var largest *repository.TagInfo
	var largestNums []int

	for _, t := range tags {
		if !re.MatchString(t.Name) && !re.MatchString(ToTag(t.Name)) {
			continue
		}
		nums := parseSemver(t.Name)
		if nums == nil {
			continue
		}
		if largestNums == nil || compareVersions(nums, largestNums) > 0 {
			t := t
			largest = &t
			largestNums = nums
		}
	}
	return largest
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