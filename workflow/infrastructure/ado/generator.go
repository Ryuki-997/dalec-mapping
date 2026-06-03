package ado

// ─── Chunk 6 · GENERATOR DETECTION ──────────────────────────────────────────

import (
	"log"

	domainRepo "dalec-mapping/domain/repository"
)

// DetectADOGenerator probes the ADO repository at the given tag for known
// build-system marker files (go.mod, Cargo.toml, etc.) and returns the
// detected SourceGenerator. When componentPath is set, the probe is scoped
// to that subdirectory; otherwise markers are probed at the repo root.
// Returns an empty SourceGenerator if no marker is found.
func DetectADOGenerator(repoURL, componentPath, tag string) domainRepo.SourceGenerator {
	for _, marker := range domainRepo.FileGeneratorMarkers {
		markerPath := marker.FileName
		if componentPath != "" {
			markerPath = componentPath + "/" + marker.FileName
		}

		if _, err := FetchADOFileContent(repoURL, markerPath, tag); err == nil {
			log.Printf("  Detected generator %s via %s\n", marker.Generator, markerPath)
			return marker.Generator
		}
	}
	return ""
}
