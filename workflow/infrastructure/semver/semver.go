// ═══════════════════════════════════════════════════════════════════════════════
// Semver — Tag resolution, filtering, pattern matching, and parsing.
//
//   Naming convention:
//     fullTag  = "azure-ipam/v0.4.0"  (prefixed tag as stored in the repo)
//     tag      = "v0.4.0"             (semver with leading "v")
//     shortTag = "0.4.0"              (semver without leading "v")
//     regexTag = "v0\.4\.\d+"         (onboard pattern that matches a family)
//
//   Chunk 1 · TAG RESOLVING  FetchRepoTags()
//   Chunk 2 · TAG FILTERING  FindLatestSpec(), FindTemplateVersion()
//   Chunk 3 · PATTERN MATCH  ResolveTagPatterns(), matchTags()
// ═══════════════════════════════════════════════════════════════════════════════

package semver

import (
	"fmt"
	"log"
	"regexp"
	"strconv"
	"strings"

	"dalec-mapping/domain/naming"
	"dalec-mapping/domain/pathcache"
	"dalec-mapping/domain/workplan"
	"dalec-mapping/workflow/infrastructure/ado"
	"dalec-mapping/workflow/infrastructure/github"
)

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

// ─── Chunk 2 · TAG FILTERING ────────────────────────────────────────────────

// FindLatestSpec scans pathcache.Cache for the highest existing specfile under
// the supplied versionPattern and returns its full pathcache key, concrete
// version, and revision.
//
// versionPattern is a raw regex fragment matching the version segment of a
// specfile path of the shape
//
//	<OnboardDir>/<SpecImageName>-<version>-<revision>-specfile.yml
//
// Callers pass either a concrete version (regexp.QuoteMeta("1.8.6")) or a
// majorMinor scan pattern (regexp.QuoteMeta("1.8") + `\.\d+`). The returned
// version is the matched concrete version extracted from the path.
//
// "Highest" means: largest version triple lexicographically (compared
// numerically component-by-component), tie-broken on revision. Returns
// ("", "", 0, false) when no specfile matches.
func FindLatestSpec(n naming.Naming, versionPattern string) (string, string, int, bool) {
	pattern := regexp.MustCompile(fmt.Sprintf(
		`^%s/%s-(%s)-(\d+)-specfile\.yml$`,
		regexp.QuoteMeta(n.OnboardDir),
		regexp.QuoteMeta(n.SpecImageName),
		versionPattern,
	))

	bestPath := ""
	bestVersion := ""
	bestRevision := 0
	var bestParts []int
	found := false
	for cachePath := range pathcache.Cache {
		matches := pattern.FindStringSubmatch(cachePath)
		if matches == nil {
			continue
		}
		candidateVersion := matches[1]
		candidateRevision, err := strconv.Atoi(matches[2])
		if err != nil {
			continue
		}
		candidateParts := parseVersionParts(candidateVersion)
		if !found || isGreater(candidateParts, candidateRevision, bestParts, bestRevision) {
			bestPath = cachePath
			bestVersion = candidateVersion
			bestRevision = candidateRevision
			bestParts = candidateParts
			found = true
		}
	}
	return bestPath, bestVersion, bestRevision, found
}

// parseVersionParts splits a numeric version like "1.8.6" into [1,8,6].
// Non-numeric segments are treated as 0 so comparison stays well-defined.
func parseVersionParts(version string) []int {
	segments := strings.Split(version, ".")
	parts := make([]int, len(segments))
	for i, segment := range segments {
		value, err := strconv.Atoi(segment)
		if err != nil {
			value = 0
		}
		parts[i] = value
	}
	return parts
}

// isGreater returns true when (candidateParts, candidateRevision) is strictly
// greater than (bestParts, bestRevision). Version parts are compared component
// by component; revision is the tie-breaker.
func isGreater(candidateParts []int, candidateRevision int, bestParts []int, bestRevision int) bool {
	limit := len(candidateParts)
	if len(bestParts) < limit {
		limit = len(bestParts)
	}
	for i := 0; i < limit; i++ {
		if candidateParts[i] != bestParts[i] {
			return candidateParts[i] > bestParts[i]
		}
	}
	if len(candidateParts) != len(bestParts) {
		return len(candidateParts) > len(bestParts)
	}
	return candidateRevision > bestRevision
}

// buildFilesRegex matches BuildFiles snapshot paths of the form
// "<OnboardDir>/buildfiles/<version>-<revision>.(df|mk)" and captures the
// concrete version and revision. One snapshot is kept per (version, revision)
// pair, sitting next to the corresponding specfile under the same OnboardDir.
func buildFilesRegex(n naming.Naming) *regexp.Regexp {
	return regexp.MustCompile(fmt.Sprintf(
		`^%s/buildfiles/(\d+(?:\.\d+)+)-(\d+)\.(?:df|mk)$`,
		regexp.QuoteMeta(n.OnboardDir),
	))
}

// FindTemplateVersion scans pathcache.Cache for the best BuildFiles snapshot
// to use as the bump-version template for the supplied work component's tag.
// The search is restricted to the same major version as the tag's intrinsic
// MajorMinor; within that major it picks the highest (version, revision)
// snapshot — whether that snapshot's minor sits above or below the tag's
// own minor.
//
// The intrinsic MajorMinor is populated by TagSet.Resolve in Phase 1 and is
// read-only here — this function never writes back to the tag. Snapshots are
// stored per-(version, revision), so the return value is the matching
// "<version>-<revision>" key (e.g. "1.8.1-1"). Callers pass that key to
// pathcache.BuildDockerfilePath / BuildMakefilePath to fetch the snapshot
// pair and to spec.BumpVersion to clone the corresponding specfile. Returns
// ("", false) when the component has no same-major BuildFiles snapshots at
// all, or when component.Tag.MajorMinor is empty.
func FindTemplateVersion(component *workplan.WorkComponent) (string, bool) {
	majorMinorParts := strings.Split(component.Tag.MajorMinor, ".")
	if len(majorMinorParts) < 2 {
		return "", false
	}
	targetMajor, err := strconv.Atoi(majorMinorParts[0])
	if err != nil {
		return "", false
	}

	pattern := buildFilesRegex(component.Naming)
	bestKey := ""
	bestRevision := 0
	var bestParts []int
	found := false
	for cachePath := range pathcache.Cache {
		matches := pattern.FindStringSubmatch(cachePath)
		if matches == nil {
			continue
		}
		candidateVersion := matches[1]
		candidateParts := parseVersionParts(candidateVersion)
		if len(candidateParts) == 0 || candidateParts[0] != targetMajor {
			continue
		}
		candidateRevision, err := strconv.Atoi(matches[2])
		if err != nil {
			continue
		}
		if !found || isGreater(candidateParts, candidateRevision, bestParts, bestRevision) {
			bestKey = fmt.Sprintf("%s-%d", candidateVersion, candidateRevision)
			bestRevision = candidateRevision
			bestParts = candidateParts
			found = true
		}
	}
	return bestKey, found
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
