package semver

import (
	"fmt"

	"dalec-mapping/infrastructure/github"
	"log"
	"regexp"
	"strconv"
	"strings"
)

func ResolveOnboardTags(repoPath string, patterns []string) ([]string, error) {
	owner, repoName, _, _ := github.ExtractRepositorySegments(repoPath)
	releaseTags, allGitTags, err := github.FetchAllTags(owner, repoName)
	if err != nil {
		log.Fatalf("❌ Failed to fetch tags: %v", err)
	}

	resolvedTags := make(map[string]bool)
	for _, pattern := range patterns {
		matched := resolvePattern(pattern, releaseTags)
		if len(matched) == 0 {
			// Check if the pattern matches git tags that have no release
			gitOnly := resolvePattern(pattern, allGitTags)
			for _, tag := range gitOnly {
				fmt.Printf("⏭  Skipping %s @ %s: tag exists but has no associated GitHub release\n", repoPath, tag)
			}
		}
		for _, resolved := range matched {
			resolvedTags[resolved] = true
		}
	}

	keys := make([]string, 0, len(resolvedTags))
	for k := range resolvedTags {
		if !strings.HasPrefix(k, "v") {
			k = "v" + k
		}
		keys = append(keys, k)
	}
	return keys, nil
}

// resolvePattern resolves a tag pattern against existing (release-filtered) tags:
//   - "latest": the single largest semver tag overall
//   - direct (e.g. v1.6.2): the tag itself if it exists (at most 1)
//   - regex  (e.g. v1\.6\.[1-9]): all matching tags that exist as releases
func resolvePattern(pattern string, existingTags []string) []string {
	if pattern == "latest" {
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

// findAllMatches returns all semver tags that match the regex.
func findAllMatches(existingTags []string, re *regexp.Regexp) []string {
	var matches []string
	for _, tag := range existingTags {
		if re.MatchString(tag) && parseSemver(tag) != nil {
			matches = append(matches, tag)
		}
	}
	return matches
}

// findLargestMatch returns the largest semver tag that matches the regex.
func findLargestMatch(existingTags []string, re *regexp.Regexp) string {
	var largest string
	var largestNums []int

	for _, tag := range existingTags {
		if !re.MatchString(tag) {
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

// parseSemver parses a tag like "v1.2.3" or "1.2.3" into [3]int, or nil if invalid.
func parseSemver(tag string) []int {
	tag = strings.TrimPrefix(tag, "v")
	parts := strings.Split(tag, ".")
	if len(parts) != 3 {
		return nil
	}
	nums := make([]int, 3)
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return nil
		}
		nums[i] = n
	}
	return nums
}

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