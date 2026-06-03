package tags

// Patterns holds inclusion and exclusion regex patterns for tag resolution.
// Include is required — at least one pattern must be present for any tags to match.
// Exclude is optional — any tag matching an exclude pattern is filtered out,
// even if it also matches an include pattern (deny wins).
type Patterns struct {
	Include []string `yaml:"include"`
	Exclude []string `yaml:"exclude,omitempty"`
}

// HasPatterns returns true when at least one include pattern is defined.
func (p Patterns) HasPatterns() bool {
	return len(p.Include) > 0
}
