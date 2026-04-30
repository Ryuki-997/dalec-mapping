package pipeline

// ═══════════════════════════════════════════════════════════════════════════════
// Pipeline — Centralized mutable state for the current (component, tag) iteration.
//
//   All workflow steps and infrastructure functions read/write from
//   pipeline.Current directly. Processing is linear — no locks needed.
//
//   Chunk 1 · STATE   State struct, Current singleton, Reset()
// ═══════════════════════════════════════════════════════════════════════════════

import (
	"dalec-mapping/domain/contents"
	"dalec-mapping/domain/onboarding"
	"dalec-mapping/domain/repository"
)

// ─── Chunk 1 · STATE ─────────────────────────────────────────────────────────

// State holds all mutable per-(component, tag) state for the pipeline.
// Set at the start of each iteration and read/written by workflow steps
// and infrastructure functions.
type State struct {
	// Onboard is the current component being processed.
	Onboard *onboarding.ComponentConfig

	// Tag is the full tag string for this iteration (e.g. "azure-ipam/v0.4.0").
	Tag string

	// RepoInfo is the resolved repository metadata for the current component.
	RepoInfo *repository.RepoInfo

	// DefaultSpec is the assembled spec combining repo, dockerfile, and onboard data.
	DefaultSpec *contents.DefaultSpec

	// PreviousSpec is the previously generated spec (for revision tracking).
	PreviousSpec contents.PreviousDalecSpec

	// Dockerfile holds the parsed AST result of the project's Dockerfile.
	Dockerfile contents.DockerfileInfo

	// Makefile holds variables extracted from the project's Makefile.
	Makefile contents.MakefileInfo

	// Spec holds the static build values derived from Dockerfile AST analysis.
	Spec *contents.DockerfileSpec
}

// Current is the singleton pipeline state for the active (component, tag) iteration.
var Current State

// Reset zeroes all fields in preparation for a new (component, tag) iteration.
func Reset() {
	Current = State{
		Dockerfile: contents.DockerfileInfo{
			Args:   make(map[string]string),
			Labels: make(map[string]string),
			Stages: []contents.Stage{},
		},
		Makefile: contents.MakefileInfo{
			Variables: make(map[string]string),
		},
	}
}
