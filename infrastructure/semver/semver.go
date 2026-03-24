package semver

import (
	"fmt"

	"log"
	"regexp"
	"strconv"
)

// fullTag is "azure-ipam/v0.4.0"
// tag is plain "v0.4.0"
// semTag is "0.4.0" (no leading "v")

// ResolveOnboardTags resolves tag patterns against pre-fetched release and git tag lists.
// releaseTags are tags with an associated release (GitHub) or annotated tags (ADO).
// allGitTags are every tag in the repo.
// Patterns are matched against releaseTags first; tags that exist only in allGitTags are skipped with a warning.
func ResolveOnboardTags(releaseTags, allGitTags []string, repoPath string, patterns []string) ([]string, error) {
	resolvedTags := make(map[string]bool)
	for _, pattern := range patterns {
		matched := resolvePattern(pattern, releaseTags)
		if len(matched) == 0 {
			// Check if the pattern matches git tags that have no release
			gitOnly := resolvePattern(pattern, allGitTags)
			for _, tag := range gitOnly {
				fmt.Printf("⏭  Skipping %s @ %s (stripped: %s): tag exists but has no associated release\n", repoPath, tag, StripToSemver(tag))
			}
		}
		for _, resolved := range matched {
			resolvedTags[resolved] = true
		}
	}

	keys := make([]string, 0, len(resolvedTags))
	for k := range resolvedTags {
		keys = append(keys, k)
	}
	return keys, nil
}

// resolvePattern resolves a tag pattern against existing (release-filtered) tags:
//   - "latest": the single largest semver tag overall (plain or suffixed)
//   - direct (e.g. v1.6.2): the tag itself if it exists (at most 1)
//   - regex  (e.g. v1\.6\.\d-main-\d+): all matching tags that exist as releases
func resolvePattern(pattern string, existingTags []string) []string {
	if pattern == "latest" {
		// Match against stripped semver — the (-.*) suffix is handled by stripToSemver in findLargestMatch.
		re := regexp.MustCompile(`^v\d+\.\d+\.\d+$`)
		largest := findLargestMatch(existingTags, re)
		if largest != "" {
			return []string{largest}
		}
		return nil
	}

	re, err := regexp.Compile("^" + pattern + "$")
	if err != nil {
		log.Printf("⚠️  Invalid regex pattern %q, skipping: %v", pattern, err)
		return nil
	}

	return findAllMatches(existingTags, re)
}

// findAllMatches returns all tags that match the regex.
// It checks the original tag first (to support prefixed patterns like "azure-ipam/v0.4.0"),
// then falls back to the stripped semver (to support plain "v0\.4\.\d+" patterns against
// prefixed tags like "azure-ipam/v0.4.0").
func findAllMatches(existingTags []string, re *regexp.Regexp) []string {
	var matches []string
	for _, tag := range existingTags {
		if re.MatchString(tag) || re.MatchString(StripToSemver(tag)) {
			matches = append(matches, tag)
		}
	}
	return matches
}

// findLargestMatch returns the largest tag that matches the regex.
// It checks the original tag first, then falls back to the stripped semver.
func findLargestMatch(existingTags []string, re *regexp.Regexp) string {
	var largest string
	var largestNums []int

	for _, tag := range existingTags {
		if !re.MatchString(tag) && !re.MatchString(StripToSemver(tag)) {
			continue
		}

		nums := parseSemver(tag)
		if nums == nil {
			continue
		}

		if largestNums == nil || compareVersions(nums, largestNums) > 0 {
			largest = tag
			largestNums = nums
		}
	}

	return largest
}

// parseSemver finds and parses the first vX.Y.Z occurrence in a tag.
// Handles plain tags ("v1.2.3"), suffixed tags ("v1.2.3-main-date-hash"),
// and tags with arbitrary english prefixes ("release-v1.2.3-extra").
// Returns nil if no valid semver is found.
func parseSemver(tag string) []int {
	m := semverInTag.FindStringSubmatch(tag)
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

// StripToSemver extracts just the "v{major}.{minor}.{patch}" portion from anywhere
// in a tag, discarding any surrounding prefix or suffix (e.g. "azure-ipam/v0.4.0" → "v0.4.0").
// Returns the original tag unchanged if no semver is found.
func StripToSemver(tag string) string {
	nums := parseSemver(tag)
	if nums == nil {
		return tag
	}
	return fmt.Sprintf("v%d.%d.%d", nums[0], nums[1], nums[2])
}

// semverInTag matches the first vX.Y.Z occurrence inside any tag string.
var semverInTag = regexp.MustCompile(`v(\d+)\.(\d+)\.(\d+)`)

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