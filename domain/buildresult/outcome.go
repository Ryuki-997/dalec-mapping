package buildresult

// Outcome classifies what Phase 2 did for a given WorkComponent.
type Outcome int

const (
	OutcomeUnknown      Outcome = iota
	OutcomeSkipped              // Spec already up to date
	OutcomeBumpVersion          // Template copied with new commit/version
	OutcomeBumpRevision         // Same version re-pushed → revision++
	OutcomeGenerated            // Fresh generation from Dockerfile/Makefile
	OutcomeFailed               // Generation failed; see Err
)

// String returns the human-readable action label for an Outcome.
func (o Outcome) String() string {
	switch o {
	case OutcomeSkipped:
		return "SKIPPED"
	case OutcomeBumpVersion:
		return "BUMP VERSION"
	case OutcomeBumpRevision:
		return "BUMP REVISION"
	case OutcomeGenerated:
		return "GENERATE"
	case OutcomeFailed:
		return "FAILED"
	}
	return "UNKNOWN"
}
