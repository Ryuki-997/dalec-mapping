package tags

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// semverRegex matches the first vX.Y.Z occurrence inside any tag string.
// Mirrors workflow/infrastructure/semver.semverRegex; kept duplicated here
// so the domain tags package has no upward dependency on workflow.
var semverRegex = regexp.MustCompile(`v(\d+)\.(\d+)\.(\d+)`)

// TagSet holds all derived representations of a single resolved tag. Every
// derived field is populated by (*TagSet).Resolve from the canonical Full
// value; callers never compute these by hand.
//
// Convention: the empty string ("") means "could not derive / not set".
// Resolve has no error path: when Full is not a valid v-semver-bearing tag
// every derived field is left at "".
type TagSet struct {
	// Full is the resolved tag string from GitHub (e.g. "azure-ipam/v0.4.0").
	// Always set at construction time; the input to Resolve.
	Full string

	// Stripped is the short semver form with v prefix (e.g. "v0.4.0").
	// Populated by Resolve from Full; empty when Full is not a valid v-semver.
	Stripped string

	// Version is the pure numeric semver without v prefix (e.g. "0.4.0").
	// Populated by Resolve from Full; empty when Full is not a valid v-semver.
	Version string

	// MajorMinor is the tag's own "<major>.<minor>" prefix (e.g. "0.4").
	// Populated by Resolve from Full; empty when Full is not a valid v-semver.
	// This is the tag's intrinsic minor, never overwritten — even when a
	// different template's minor is selected for a BUMP VERSION.
	MajorMinor string
}

// Resolve fills Stripped, Version, and MajorMinor from Full. It is the
// single TrimPrefix("v") site in the codebase. Idempotent: calling it twice
// leaves the derived fields unchanged. No error is returned; callers detect
// "no semver" via empty derived fields.
func (t *TagSet) Resolve() {
	match := semverRegex.FindStringSubmatch(t.Full)
	if match == nil {
		return
	}

	major, err := strconv.Atoi(match[1])
	if err != nil {
		return
	}
	minor, err := strconv.Atoi(match[2])
	if err != nil {
		return
	}
	patch, err := strconv.Atoi(match[3])
	if err != nil {
		return
	}

	t.Stripped = fmt.Sprintf("v%d.%d.%d", major, minor, patch)
	t.Version = strings.TrimPrefix(t.Stripped, "v")
	t.MajorMinor = fmt.Sprintf("%d.%d", major, minor)
}
