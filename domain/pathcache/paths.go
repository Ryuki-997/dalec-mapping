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
// under <OnboardDir>/buildfiles/<majorMinor>/. A single snapshot directory
// is shared by every patch on the same minor, so callers pass the already-
// computed "<major>.<minor>" string (TagSet.MajorMinor, or the template
// minor resolved by semver.FindTemplateVersion).
// Returns "" when majorMinor is empty.
func BuildDockerfilePath(n naming.Naming, majorMinor string) string {
	if majorMinor == "" {
		return ""
	}
	return fmt.Sprintf("%s/buildfiles/%s/%s.df", n.OnboardDir, majorMinor, n.SpecImageName)
}

// BuildMakefilePath returns the snapshot path for a component's Makefile
// under <OnboardDir>/buildfiles/<majorMinor>/. A single snapshot directory
// is shared by every patch on the same minor, so callers pass the already-
// computed "<major>.<minor>" string (TagSet.MajorMinor, or the template
// minor resolved by semver.FindTemplateVersion).
// Returns "" when majorMinor is empty.
func BuildMakefilePath(n naming.Naming, majorMinor string) string {
	if majorMinor == "" {
		return ""
	}
	return fmt.Sprintf("%s/buildfiles/%s/%s.mk", n.OnboardDir, majorMinor, n.SpecImageName)
}
