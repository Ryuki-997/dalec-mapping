package pathcache

import (
	"fmt"
	"strings"

	"dalec-mapping/config"
	"dalec-mapping/domain/naming"
)

// OnboardAPIPath builds a GitHub REST path scoped to the managed spec
// repository (config.OnboardOwner/config.OnboardRepo). suffixFmt is treated
// as a fmt format string applied to args.
//
// Example:
//
//	OnboardAPIPath("issues/%d/labels", prNumber)
//	→ "repos/<onboardOwner>/<onboardRepo>/issues/123/labels"
func OnboardAPIPath(suffixFmt string, args ...any) string {
	base := "repos/" + config.OnboardOwner + "/" + config.OnboardRepo
	if suffixFmt == "" {
		return base
	}
	return base + "/" + fmt.Sprintf(suffixFmt, args...)
}

// BuildDockerfilePath returns the snapshot path for a component's Dockerfile
// under <OnboardDir>/buildfiles/<majorMinor>/<versionRevision>.df. One
// snapshot is kept per (version, revision) of an existing spec, bucketed by
// the version's major.minor directory. Callers pass the already-computed
// "<version>-<revision>" string (Naming.VersionRevision for a fresh commit,
// or the template key resolved by semver.FindTemplateVersion); the
// "<major>.<minor>" parent directory is derived from versionRevision.
// Returns "" when versionRevision is empty or malformed.
func BuildDockerfilePath(n naming.Naming, versionRevision string) string {
	majorMinor, ok := majorMinorFromVersionRevision(versionRevision)
	if !ok {
		return ""
	}
	return fmt.Sprintf("%s/buildfiles/%s/%s.df", n.OnboardDir, majorMinor, versionRevision)
}

// BuildMakefilePath returns the snapshot path for a component's Makefile
// under <OnboardDir>/buildfiles/<majorMinor>/<versionRevision>.mk. See
// BuildDockerfilePath for the directory derivation rules.
// Returns "" when versionRevision is empty or malformed.
func BuildMakefilePath(n naming.Naming, versionRevision string) string {
	majorMinor, ok := majorMinorFromVersionRevision(versionRevision)
	if !ok {
		return ""
	}
	return fmt.Sprintf("%s/buildfiles/%s/%s.mk", n.OnboardDir, majorMinor, versionRevision)
}

// majorMinorFromVersionRevision extracts the "<major>.<minor>" prefix from a
// "<version>-<revision>" key like "1.8.1-1" → "1.8". Returns ("", false) when
// the input is empty, has no '-' separator, or whose version segment has fewer
// than two dot-separated parts.
func majorMinorFromVersionRevision(versionRevision string) (string, bool) {
	if versionRevision == "" {
		return "", false
	}
	dashIndex := strings.IndexByte(versionRevision, '-')
	if dashIndex <= 0 {
		return "", false
	}
	version := versionRevision[:dashIndex]
	parts := strings.SplitN(version, ".", 3)
	if len(parts) < 2 {
		return "", false
	}
	return parts[0] + "." + parts[1], true
}
