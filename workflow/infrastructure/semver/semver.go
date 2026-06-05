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
//   Chunk 2 · TAG FILTERING  FindLatestRevision(), FindTemplateVersion()
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
	"dalec-mapping/domain/pathcache"
	"dalec-mapping/domain/workplan"
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

// ─── Chunk 2 · TAG FILTERING ────────────────────────────────────────────────

// FindLatestRevision scans pathcache.Cache for the highest revision number
// of a spec file matching {n.SpecImageName}-{version}-{n}-specfile.yml.
// version must already be in numeric form (no leading "v"); the canonical
// source is TagSet.Version. Returns (0, false) when no matching revision
// exists.
func FindLatestRevision(n naming.Naming, version string) (int, bool) {
	pattern := n.SpecFilePathRegex(regexp.QuoteMeta(version), `(\d+)`)

	highest := 0
	found := false
	for path := range pathcache.Cache {
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

// buildFilesRegex matches BuildFiles snapshot paths of the form
// "<OnboardDir>/buildfiles/<X>.<Y>/<SpecImageName>.(df|mk)" and captures
// the major and minor version numbers. A single directory is shared across
// every patch on the same minor, so the regex does not include a patch
// segment.
func buildFilesRegex(n naming.Naming) *regexp.Regexp {
	return regexp.MustCompile(fmt.Sprintf(
		`^%s/buildfiles/(\d+)\.(\d+)/%s\.(?:df|mk)$`,
		regexp.QuoteMeta(n.OnboardDir),
		regexp.QuoteMeta(n.SpecImageName),
	))
}

// FindTemplateVersion scans pathcache.Cache for the best BuildFiles snapshot
// to use as the bump-version template for the supplied work component's tag. The
// search is restricted to the same major version as the tag's intrinsic
// MajorMinor and proceeds in two stages:
//
//  1. Preferred: exact major.minor match of component.Tag.MajorMinor.
//  2. Fallback: largest existing minor within the same major — whether
//     that minor sits above or below the tag's own minor. The "largest
//     existing minor" semantics let us pick e.g. snapshot 1.8 as the
//     template for tag v1.7.x when 1.7 has no snapshot of its own and
//     1.8 is the only sibling.
//
// The intrinsic MajorMinor is populated by TagSet.Resolve in Phase 1 and is
// read-only here — this function never writes back to the tag. Snapshots
// are stored per-minor (one directory shared by every patch), so the return
// value is the "<major>.<minor>" prefix (e.g. "1.6"), not a full semver.
// Returns ("", false) when the component has no same-major BuildFiles
// snapshots at all, or when component.Tag.MajorMinor is empty.
func FindTemplateVersion(component *workplan.WorkComponent) (string, bool) {
	parts := strings.Split(component.Tag.MajorMinor, ".")
	if len(parts) < 2 {
		return "", false
	}
	targetMajor, err := strconv.Atoi(parts[0])
	if err != nil {
		return "", false
	}
	targetMinor, err := strconv.Atoi(parts[1])
	if err != nil {
		return "", false
	}

	pattern := buildFilesRegex(component.Naming)
	minorsForMajor := make(map[int]bool)
	for path := range pathcache.Cache {
		matches := pattern.FindStringSubmatch(path)
		if matches == nil {
			continue
		}
		major, err := strconv.Atoi(matches[1])
		if err != nil || major != targetMajor {
			continue
		}
		minor, err := strconv.Atoi(matches[2])
		if err != nil {
			continue
		}
		minorsForMajor[minor] = true
	}

	if minorsForMajor[targetMinor] {
		return fmt.Sprintf("%d.%d", targetMajor, targetMinor), true
	}

	bestMinor := -1
	for minor := range minorsForMajor {
		if minor > bestMinor {
			bestMinor = minor
		}
	}
	if bestMinor == -1 {
		return "", false
	}
	return fmt.Sprintf("%d.%d", targetMajor, bestMinor), true
}

// FindLatestVersionAndRevision scans pathcache.Cache for the highest-patch
// specfile under the supplied "<major>.<minor>" prefix and, within that
// patch, the highest revision. Used by spec.BumpVersion to resolve the
// concrete template specfile from a minor-only snapshot directory.
//
// Returns (version, revision, true) where version is the full "X.Y.Z" of
// the template specfile, or ("", 0, false) when no matching specfile exists.
func FindLatestVersionAndRevision(n naming.Naming, majorMinor string) (string, int, bool) {
	versionPattern := regexp.QuoteMeta(majorMinor) + `\.(\d+)`
	pattern := n.SpecFilePathRegex(versionPattern, `(\d+)`)

	bestPatch := -1
	bestRevision := 0
	for path := range pathcache.Cache {
		matches := pattern.FindStringSubmatch(path)
		if matches == nil {
			continue
		}
		patch, err := strconv.Atoi(matches[1])
		if err != nil {
			continue
		}
		revision, err := strconv.Atoi(matches[2])
		if err != nil {
			continue
		}
		if patch > bestPatch || (patch == bestPatch && revision > bestRevision) {
			bestPatch = patch
			bestRevision = revision
		}
	}

	if bestPatch < 0 {
		return "", 0, false
	}
	return fmt.Sprintf("%s.%d", majorMinor, bestPatch), bestRevision, true
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
