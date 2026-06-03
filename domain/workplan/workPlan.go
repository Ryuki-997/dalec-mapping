package workplan

// WorkPlan is the complete flat list of items produced by Phase 1, plus
// the set of spec paths that already exist on the remote (used by Phase 2
// to decide skip/bump/generate).
type WorkPlan struct {
	Items         []WorkItem
	ExistingPaths map[string]bool
}
