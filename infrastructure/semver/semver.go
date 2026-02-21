package semver

import (
	"dalec-mapping/infrastructure/github"
	"log"
	"strconv"
	"strings"
)


func ResolveOnboardTags(repoPath string, tags []string) ([]string, error) {
	owner, repoName, _, _ := github.ExtractRepositorySegments(repoPath)
	allTags, err := github.FetchAllTags(owner, repoName)
	if err != nil {
		log.Fatalf("❌ Failed to fetch tags: %v", err)
	}

	existingTags := make(map[string]bool)
	for _, tag := range allTags {
		existingTags[strings.TrimPrefix(tag, "v")] = true
	}

	resolvedTags := make(map[string]bool)
	for _, tag := range tags {
		resolved := getCorrespondingTag(tag, existingTags)
		if resolved != "" && !resolvedTags[resolved] {
			resolvedTags[resolved] = true
		}
	}

	// Get unique resolved tags as a slice
	keys := make([]string, 0, len(resolvedTags))
	for k := range resolvedTags {
		keys = append(keys, "v" + k)
	}
	return keys, nil
}

func getCorrespondingTag(tag string, existingTags map[string]bool) string {
	// Strip leading "v" prefix to normalize (e.g. v0.2.10 -> 0.2.10)
	tag = strings.TrimPrefix(tag, "v")

	// Exact match
	if existingTags[tag] {
		return tag
	}

	// "latest" → return the overall largest tag (all wildcards)
	if tag == "latest" {
		return findLargestTag(existingTags, "*", "*", "*")
	}

	parts := strings.Split(tag, ".")
	for len(parts) < 3 {
		parts = append(parts, "*")
	}

	// If no wildcards remain, it's an exact tag that doesn't exist — skip
	hasWildcard := false
	for _, p := range parts {
		if isWildcard(p) {
			hasWildcard = true
			break
		}
	}
	if !hasWildcard {
		return ""
	}

	return findLargestTag(existingTags, parts[0], parts[1], parts[2])
}

func isWildcard(s string) bool {
	return s == "x" || s == "X" || s == "*"
}

// findLargestTag finds the largest semver tag filtered by major/minor/patch.
// Wildcard values ("x", "X", "*") match any component.
func findLargestTag(existingTags map[string]bool, major, minor, patch string) string {
	var largest string
	var largestNums []int

	filters := []string{major, minor, patch}

	for tag := range existingTags {
		parts := strings.Split(tag, ".")
		if len(parts) != 3 {
			continue
		}

		match := true
		nums := make([]int, 3)
		for i, p := range parts {
			n, err := strconv.Atoi(p)
			if err != nil {
				match = false
				break
			}
			nums[i] = n
			if !isWildcard(filters[i]) && parts[i] != filters[i] {
				match = false
				break
			}
		}
		if !match {
			continue
		}

		if largestNums == nil || compareVersions(nums, largestNums) > 0 {
			largest = tag
			largestNums = nums
		}
	}

	return largest
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