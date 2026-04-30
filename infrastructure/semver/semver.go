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

// resolveGithubTags fetches all git tags from GitHub and matches patterns against them.
func resolveGithubTags(repoURL string, regexTags []string) ([]repository.TagInfo, error) {
	owner, repoName, _ := repository.FetchRepositorySegments(repoURL)
	allTags, err := repository.FetchAllGithubTags(owner, repoName)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch tags for %s: %w", repoURL, err)
	}

	return matchPatterns(allTags, regexTags), nil
}

// ─── Chunk 2 · TAG FILTERING ────────────────────────────────────────────────

// ActionableTag represents a tag that needs processing, with its next revision number.
type ActionableTag struct {
	repository.TagInfo
	NextRevision int // The revision number to create (e.g. 1 for new, 2 for second revision)
}

// FilterActionableTags returns tags that need processing: either they have no
// existing revisions, or their latest revision's commit differs from the tag's
// current commit SHA. Each returned tag carries the next revision number to use.
func FilterActionableTags(tags []repository.TagInfo, specDir, specImage, owner, repo, branch string, treePaths map[string]bool) []ActionableTag {
	var actionable []ActionableTag
	for _, tagInfo := range tags {
		tag := ToTag(tagInfo.Name)
		latestRevision, found := FindLatestRevision(specDir, specImage, tag, treePaths)

		if !found {
			// No existing spec — first revision
			actionable = append(actionable, ActionableTag{
				TagInfo:      tagInfo,
				NextRevision: 1,
			})
			continue
		}

		// Check if the latest revision's commit matches the tag's current commit
		latestSpecPath := SpecFilePath(specDir, specImage, tag, latestRevision)
		existingCommit, err := repository.FetchRemoteSpecCommit(latestSpecPath, owner, repo, branch)
		if err != nil {
			log.Printf("⚠️  Could not read commit from %s: %v — treating as actionable\n", latestSpecPath, err)
			actionable = append(actionable, ActionableTag{
				TagInfo:      tagInfo,
				NextRevision: latestRevision + 1,
			})
			continue
		}

		if existingCommit != tagInfo.Commit {
			log.Printf("Tag %s commit changed (%s → %s) — scheduling R%d\n",
				tagInfo.Name, existingCommit[:8], tagInfo.Commit[:8], latestRevision+1)
			actionable = append(actionable, ActionableTag{
				TagInfo:      tagInfo,
				NextRevision: latestRevision + 1,
			})
		} else {
			log.Printf("Skipping %s @ %s: spec R%d already up to date\n", specImage, tagInfo.Name, latestRevision)
		}
	}
	return actionable
}

// FilterUpToDateTags returns tags whose latest revision's commit matches the
// tag's current commit (i.e. no work needed). Used to find template specs.
func FilterUpToDateTags(tags []repository.TagInfo, specDir, specImage string, treePaths map[string]bool) []repository.TagInfo {
	var upToDate []repository.TagInfo
	for _, tagInfo := range tags {
		tag := ToTag(tagInfo.Name)
		_, found := FindLatestRevision(specDir, specImage, tag, treePaths)
		if found {
			upToDate = append(upToDate, tagInfo)
		}
	}
	return upToDate
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
