package workplan

import (
	"dalec-mapping/domain/buildresult"
	"dalec-mapping/domain/contents"
	"dalec-mapping/domain/naming"
	"dalec-mapping/domain/onboarding"
	"dalec-mapping/domain/repository"
	"dalec-mapping/domain/tags"
)

// WorkItemGroup is one pull request's worth of work: every WorkItem whose
// onboard component shares the same GroupName, plus the single PRID minted
// once at group creation in Phase 1. Items are pointers so Phase 2 can write
// the per-item Result without touching the surrounding group/plan structure.
type WorkItemGroup struct {
	GroupName string
	PRID      string
	Items     []*WorkItem
}

// WorkItem is the unit processed by the pipeline.
//
//   - Identity (Naming, Component, Tag, BuildFiles) is populated by Phase 1
//     and is read-only afterwards. Phase 1 calls Naming.Construct exactly
//     once per item with the group's PRID so every Generated field —
//     including BranchName/PRTitle — is final at the end of Phase 1.
//   - Result is the only field Phase 2 writes; it carries the BuildResult
//     produced for this item. Phase 3 and observers read Result and treat
//     every other field as immutable.
//
// Functions take *WorkItem to avoid copying derived data and to make the
// data flow explicit at every call site (no ambient package globals).
type WorkItem struct {
	Naming     naming.Naming
	Component  onboarding.OnboardingComponent
	Tag        tags.Set
	BuildFiles BuildFilesInfo
	Result     buildresult.BuildResult
}

// BuildFilesInfo holds everything derived from the partner repo at a specific
// tag: raw source bytes, the parsed Dockerfile/Makefile metadata, upstream
// repository information, and the static build values extracted from the
// Dockerfile. Populated incrementally by Phase 2 sub-steps.
type BuildFilesInfo struct {
	Dockerfile contents.DockerfileInfo // parsed Dockerfile incl. raw Source bytes (discover sets Source; parser fills the rest)
	Makefile   contents.MakefileInfo   // parsed Makefile incl. raw Source bytes (discover sets Source; parser fills the rest)
	Spec       contents.DockerSpec     // populated once by spec.GenerateSpec via parser.ExtractStaticBuildValues; read-only afterwards
	RepoInfo   repository.RepoInfo     // populated once by spec.GenerateSpec via buildRepoInfo; read-only afterwards
}
