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
	"fmt"

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

	// Tag holds all derived representations of the current tag.
	Tag onboarding.TagSet

	// RepoInfo is the resolved repository metadata for the current component.
	RepoInfo *repository.RepoInfo

	// Dockerfile holds the parsed AST result of the project's Dockerfile.
	Dockerfile contents.DockerfileInfo

	// Makefile holds variables extracted from the project's Makefile.
	Makefile contents.MakefileInfo

	// Spec holds the static build values derived from Dockerfile AST analysis.
	Spec *contents.DockerfileSpec
}

// Current is the singleton pipeline state for the active (component, tag) iteration.
var Current State

// TagCache maps repoURL → tagName → commitSHA.
// Populated once during step 1 tag fetching, then read by later steps
// (e.g. step 3 bump-commit) to avoid redundant API calls.
var TagCache map[string]map[string]string

// InitTagCache creates an empty tag cache. Call once before step 1.
func InitTagCache() {
	TagCache = make(map[string]map[string]string)
}

// LookupTagCommit returns the cached commit SHA for the given repo URL and tag.
// Returns an error if the repo or tag is not in the cache.
func LookupTagCommit(repoURL, fullTag string) (string, error) {
	repoTags, ok := TagCache[repoURL]
	if !ok {
		return "", fmt.Errorf("no cached tags for repo %s", repoURL)
	}
	commitSHA, ok := repoTags[fullTag]
	if !ok {
		return "", fmt.Errorf("tag %s not found in cache for repo %s", fullTag, repoURL)
	}
	return commitSHA, nil
}

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
