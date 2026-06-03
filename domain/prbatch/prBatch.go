package prbatch

import (
	"fmt"

	"dalec-mapping/domain/buildresult"
	"dalec-mapping/domain/naming"
)

// PRGroupKey identifies one PR. Multiple WorkItems collapse into one PR when
// they share the same group name and tag.
type PRGroupKey struct {
	GroupName string // onboard.GroupName if set, else SpecImageName
	Tag       string // TagSet.Stripped
}

// String returns a stable string form for logging / map keys.
func (k PRGroupKey) String() string {
	return fmt.Sprintf("%s@%s", k.GroupName, k.Tag)
}

// BatchComponent is one component's contribution to a PR batch, paired with
// the resolved naming (computed after the batch's PRID is assigned).
type BatchComponent struct {
	Result buildresult.BuildResult
	Naming naming.Naming
}

// PRBatch is the grouped output of Phase 3's grouping step. All components
// in a batch land in the same pull request.
type PRBatch struct {
	Key        PRGroupKey
	PRID       string
	Components []BatchComponent
}
