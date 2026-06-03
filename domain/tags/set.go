package tags

import "strings"

// Set holds all derived representations of a single resolved tag.
type Set struct {
	// Pattern is the customer-provided regex pattern that matched this tag (e.g. "azure-ipam/v0\\.4\\..*").
	Pattern string

	// Full is the resolved tag string from GitHub (e.g. "azure-ipam/v0.4.0").
	Full string

	// Stripped is the short semver form with v prefix (e.g. "v0.4.0").
	Stripped string

	// Version is the pure numeric semver without v prefix (e.g. "0.4.0").
	Version string

	// Revision is the next spec revision number for this tag (e.g. 1, 2, 3).
	Revision int
}

// NewSet constructs a Set from a full tag, its matching pattern, and stripped form.
// Derives the version (X.Y.Z) by trimming the v prefix from the stripped tag.
func NewSet(fullTag, pattern, strippedTag string, revision int) Set {
	version := strings.TrimPrefix(strippedTag, "v")
	return Set{
		Pattern:  pattern,
		Full:     fullTag,
		Stripped: strippedTag,
		Version:  version,
		Revision: revision,
	}
}
