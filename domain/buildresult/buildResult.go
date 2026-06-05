package buildresult

// BuildResult is the per-component output of Phase 2 stored on WorkComponent.Result.
// SpecContent is populated when Outcome is BumpVersion/BumpRevision/Generated.
type BuildResult struct {
	Outcome     Outcome
	SpecContent []byte
}

// IsPublishable reports whether the result should be included in a PR batch.
func (r BuildResult) IsPublishable() bool {
	switch r.Outcome {
	case OutcomeBumpVersion, OutcomeBumpRevision, OutcomeGenerated:
		return true
	}
	return false
}
