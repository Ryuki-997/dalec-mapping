package transformer

// ═══════════════════════════════════════════════════════════════════════════════
// extractArtifacts.go — Generates the `artifacts:` section of a Dalec spec.
//
//   Chunk 1 · ORCHESTRATION            extractArtifactsSection()
//     Assembles the global artifacts map: Linux binaries + license.
//     Calls → computeArtifactPaths()
//
//   Chunk 2 · PATH RESOLUTION          computeArtifactPaths()
//     Determines which built files become RPM artifacts.
//     Priority: wrapper pipeline output > per-binary outputs > fallback.
//     Calls → lastGoBuildOutputInPipeline(), resolveOutputPath(),
//             entrypointBinaryName(), canonicalBase()
//
//   Chunk 3 · WINDOWS ARTIFACTS        computeWindowsArtifactBinaries()
//     Derives .exe artifact paths from Linux paths (used by extractTargets).
//
//   Chunk 4 · UTILITIES                resolveOutputPath(), lastGoBuildOutputInPipeline()
//     Helpers for extracting output paths from binary fields and pipeline steps.
// ═══════════════════════════════════════════════════════════════════════════════

import (
	"dalec-mapping/domain/contents"

	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

// goBuildOutputRe matches `-o /go/bin/<name>` in go build commands.
var goBuildOutputRe = regexp.MustCompile(`-o\s+(/go/bin/[^\s]+)`)

// ─── Chunk 1 · ORCHESTRATION ─────────────────────────────────────────────────

// extractArtifactsSection returns the global artifacts section (Linux binaries + license).
func extractArtifactsSection(defaultSpec *contents.DefaultSpec) map[string]interface{} {
	binaries := make(map[string]interface{})
	for path := range computeArtifactPaths(defaultSpec) {
		binaries[path] = map[string]interface{}{}
	}
	return map[string]interface{}{
		"binaries": binaries,
		"licenses": map[string]interface{}{
			defaultSpec.Repo + "/LICENSE": map[string]interface{}{},
		},
	}
}

// ─── Chunk 2 · PATH RESOLUTION ──────────────────────────────────────────────

// computeArtifactPaths returns the Linux binary artifact paths (no .exe).
func computeArtifactPaths(defaultSpec *contents.DefaultSpec) map[string]interface{} {
	paths := make(map[string]interface{})

	// Wrapper pipeline: the LAST go build output from pipeline steps is the final artifact.
	if contents.Spec != nil && len(contents.Spec.PipelineSteps) > 0 {
		if wrapperPath := lastGoBuildOutputInPipeline(contents.Spec.PipelineSteps); wrapperPath != "" {
			paths[filepath.ToSlash(wrapperPath)] = struct{}{}
			fmt.Printf("ARTIFACTS: %v\n", wrapperPath)
			return paths
		}
	}

	// Standard case: derive from binaries.
	if contents.Spec != nil && len(contents.Spec.Binaries) > 0 {
		epBase := entrypointBinaryName(contents.Spec)
		for _, bin := range contents.Spec.Binaries {
			p := resolveOutputPath(bin)
			// Single-binary rename when entrypoint differs.
			if epBase != "" && canonicalBase(p) != epBase && len(contents.Spec.Binaries) == 1 {
				p = "/go/bin/" + epBase
			}
			artifact := filepath.ToSlash(p)
			paths[artifact] = struct{}{}
			fmt.Printf("ARTIFACTS: %v\n", artifact)
		}
		return paths
	}

	// Fallback.
	paths["/go/bin/"+defaultSpec.Repo] = struct{}{}
	fmt.Printf("ARTIFACTS: /go/bin/%s\n", defaultSpec.Repo)
	return paths
}

// ─── Chunk 3 · WINDOWS ARTIFACTS ────────────────────────────────────────────

// computeWindowsArtifactBinaries returns the windowscross artifact binaries map.
// Appends ".exe" to each Linux artifact key — matches the file written by the
// BIN_SUFFIX build step when GOOS=windows.
func computeWindowsArtifactBinaries(defaultSpec *contents.DefaultSpec) map[string]interface{} {
	binaries := make(map[string]interface{})
	for linuxPath := range computeArtifactPaths(defaultSpec) {
		binaries[linuxPath+".exe"] = map[string]interface{}{}
	}
	return binaries
}

// ─── Chunk 4 · UTILITIES ────────────────────────────────────────────────────

// resolveOutputPath derives the artifact output path from a binary's fields.
func resolveOutputPath(bin contents.SpecBinary) string {
	if bin.OutputPath != "" {
		return bin.OutputPath
	}
	if flagPath := extractOutputFlag(bin.BuildCommand); flagPath != "" {
		return flagPath
	}
	return "/go/bin/" + bin.Name
}

// lastGoBuildOutputInPipeline scans pipeline steps for `go build -o /go/bin/<name>`
// and returns the LAST output path found (the wrapper binary / final artifact).
func lastGoBuildOutputInPipeline(steps []string) string {
	var last string
	for _, step := range steps {
		for _, line := range strings.Split(step, "\n") {
			if m := goBuildOutputRe.FindStringSubmatch(line); m != nil {
				last = m[1]
			}
		}
	}
	return last
}
