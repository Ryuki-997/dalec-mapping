package pathcache

import (
	"fmt"

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
// under <OnboardDir>/buildfiles/<versionRevision>.df. One snapshot is kept
// per (version, revision) of an existing spec, so callers pass the already-
// computed "<version>-<revision>" string (Naming.VersionRevision for a fresh
// commit, or the template key resolved by semver.FindTemplateVersion).
// Returns "" when versionRevision is empty.
func BuildDockerfilePath(n naming.Naming, versionRevision string) string {
	if versionRevision == "" {
		return ""
	}
	return fmt.Sprintf("%s/buildfiles/%s.df", n.OnboardDir, versionRevision)
}

// BuildMakefilePath returns the snapshot path for a component's Makefile
// under <OnboardDir>/buildfiles/<versionRevision>.mk. One snapshot is kept
// per (version, revision) of an existing spec, so callers pass the already-
// computed "<version>-<revision>" string (Naming.VersionRevision for a fresh
// commit, or the template key resolved by semver.FindTemplateVersion).
// Returns "" when versionRevision is empty.
func BuildMakefilePath(n naming.Naming, versionRevision string) string {
	if versionRevision == "" {
		return ""
	}
	return fmt.Sprintf("%s/buildfiles/%s.mk", n.OnboardDir, versionRevision)
}
