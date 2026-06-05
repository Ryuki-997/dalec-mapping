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
//             canonicalBase()
//
//   Chunk 3 · WINDOWS ARTIFACTS        computeWindowsArtifactBinaries()
//     Derives .exe artifact paths from Linux paths (used by extractTargets).
//
//   Chunk 4 · UTILITIES                resolveOutputPath(), lastGoBuildOutputInPipeline()
//     Helpers for extracting output paths from binary fields and pipeline steps.
// ═══════════════════════════════════════════════════════════════════════════════

import (
	"dalec-mapping/domain/contents"
	"dalec-mapping/domain/workplan"

	"path/filepath"
	"regexp"
	"strings"
)

// goBuildOutputRe matches `-o /go/bin/<name>` in go build commands.
var goBuildOutputRe = regexp.MustCompile(`-o\s+(/go/bin/[^\s]+)`)

// ─── Chunk 1 · ORCHESTRATION ─────────────────────────────────────────────────

// extractArtifactsSection returns the global artifacts section (Linux binaries + license).
func extractArtifactsSection(component *workplan.WorkComponent) map[string]interface{} {
	binaries := make(map[string]interface{})
	for path := range computeArtifactPaths(component) {
		binaries[path] = map[string]interface{}{}
	}
	return map[string]interface{}{
		"binaries": binaries,
		"licenses": map[string]interface{}{
			component.BuildFiles.RepoInfo.Repo + "/LICENSE": map[string]interface{}{},
		},
	}
}

// ─── Chunk 2 · PATH RESOLUTION ─────────────────────────────────

// computeArtifactPaths returns the Linux binary artifact paths (no .exe).
func computeArtifactPaths(component *workplan.WorkComponent) map[string]interface{} {
	paths := make(map[string]interface{})

	// Wrapper pipeline: the LAST go build output from pipeline steps is the final artifact.
	if len(component.BuildFiles.Spec.PipelineSteps) > 0 {
		if wrapperPath := lastGoBuildOutputInPipeline(component.BuildFiles.Spec.PipelineSteps); wrapperPath != "" {
			paths[filepath.ToSlash(wrapperPath)] = struct{}{}
			return paths
		}
	}

	// Standard case: derive from binaries.
	if len(component.BuildFiles.Spec.Binaries) > 0 {
		epBase := canonicalBase(component.BuildFiles.Spec.Symlink)
		for _, bin := range component.BuildFiles.Spec.Binaries {
			p := resolveOutputPath(bin)
			// Single-binary rename when entrypoint differs.
			if epBase != "" && canonicalBase(p) != epBase && len(component.BuildFiles.Spec.Binaries) == 1 {
				p = "/go/bin/" + epBase
			}
			artifact := filepath.ToSlash(p)
			paths[artifact] = struct{}{}
		}
		return paths
	}

	// Fallback: use Makefile binary name if available, then component name, otherwise repo name.
	if len(component.BuildFiles.Makefile.GoBuildCommands) > 0 && component.BuildFiles.Makefile.GoBuildCommands[0].Name != "" {
		paths["/go/bin/"+component.BuildFiles.Makefile.GoBuildCommands[0].Name] = struct{}{}
	} else {
		binaryName := component.BuildFiles.RepoInfo.Repo
		if component.Name != "" {
			binaryName = component.Name
		}
		paths["/go/bin/"+binaryName] = struct{}{}
	}
	return paths
}

// ─── Chunk 3 · WINDOWS ARTIFACTS ────────────────────────────────────────────

// computeWindowsArtifactBinaries returns the windowscross artifact binaries map.
// Appends ".exe" to each Linux artifact key — matches the file written by the
// BIN_SUFFIX build step when GOOS=windows.
func computeWindowsArtifactBinaries(component *workplan.WorkComponent) map[string]interface{} {
	binaries := make(map[string]interface{})
	for linuxPath := range computeArtifactPaths(component) {
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
	if m := outputFlagRe.FindStringSubmatch(bin.BuildCommand); m != nil {
		return m[1]
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
