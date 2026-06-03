package buildresult

import "dalec-mapping/domain/workplan"

// BuildResult is the per-item output of Phase 2. Always returned (never nil)
// so callers can iterate without nil checks; check Outcome / Err for status.
type BuildResult struct {
	Item        workplan.WorkItem
	Outcome     Outcome
	SpecContent []byte // populated when Outcome is BumpVersion/BumpRevision/Generated
	Err         error  // populated when Outcome is Failed
}

// IsPublishable reports whether the result should be included in a PR batch.
func (r BuildResult) IsPublishable() bool {
	switch r.Outcome {
	case OutcomeBumpVersion, OutcomeBumpRevision, OutcomeGenerated:
		return true
	}
	return false
}
