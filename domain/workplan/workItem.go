package workplan

import (
	"dalec-mapping/domain/contents"
	"dalec-mapping/domain/naming"
	"dalec-mapping/domain/repository"
	"dalec-mapping/domain/tags"
)

// WorkItem is the unit processed by the pipeline.
//
//   - Identity (Naming, Tag) is populated by Phase 1 and is read-only afterwards.
//   - BuildFiles is empty after Phase 1 and is populated incrementally during
//     the Phase 2 sub-steps (discover → parse → fetch repo metadata → extract
//     static build values). Each sub-step accepts *WorkItem and writes to the
//     fields it owns; later sub-steps read from earlier ones.
//
// Functions take *WorkItem to avoid copying derived data and to make the
// data flow explicit at every call site (no ambient package globals).
type WorkItem struct {
	Naming     naming.Naming
	Tag        tags.Set
	BuildFiles BuildFilesInfo
}

// BuildFilesInfo holds everything derived from the partner repo at a specific
// tag: raw source bytes, the parsed Dockerfile/Makefile metadata, upstream
// repository information, and the static build values extracted from the
// Dockerfile. Populated incrementally by Phase 2 sub-steps.
type BuildFilesInfo struct {
	Dockerfile contents.DockerfileInfo // parsed Dockerfile incl. raw Source bytes (discover sets Source; parser fills the rest)
	Makefile   contents.MakefileInfo   // parsed Makefile incl. raw Source bytes (discover sets Source; parser fills the rest)
	Spec       *contents.DockerSpec    // static build values (parser)
	RepoInfo   *repository.RepoInfo    // upstream repo metadata (generate)
}
