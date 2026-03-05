package transformer

import (
	"dalec-mapping/domain/contents"
	"dalec-mapping/domain/llm"
	"dalec-mapping/infrastructure/github"

	"fmt"
	"path/filepath"
)

// computeArtifactPaths returns the Linux binary artifact paths (no .exe).
// cleanOutputPath always produces /go/bin/<name>, so paths are always absolute.
func computeArtifactPaths(defaultSpec *contents.DefaultSpec, nonDeterministicValues *llm.NonDeterministicValues) map[string]interface{} {
	paths := make(map[string]interface{})

	addPath := func(outputPath string) {
		if outputPath == "" {
			return
		}
		artifact := filepath.ToSlash(outputPath)
		paths[artifact] = struct{}{}
		fmt.Printf("ARTIFACTS: %v\n", artifact)
	}

	if nonDeterministicValues != nil && len(nonDeterministicValues.Binaries) > 0 {
		for _, aux := range nonDeterministicValues.Binaries {
			outputPath := aux.OutputPath
			github.ClearEnvVariables("OutputPath", &outputPath)

			// Derive from -o flag if outputPath not set (safety fallback)
			if outputPath == "" {
				if flagPath := extractOutputFlag(aux.BuildCommand); flagPath != "" {
					outputPath = flagPath
				} else {
					outputPath = "/go/bin/" + aux.Name
				}
			}

			addPath(outputPath)
		}
	} else {
		addPath("/go/bin/" + defaultSpec.Repo)
	}
	return paths
}

// computeWindowsArtifactBinaries returns the windowscross artifact binaries map.
// Appends ".exe" to each Linux artifact key — matches the file written by the
// BIN_SUFFIX build step when GOOS=windows.
func computeWindowsArtifactBinaries(defaultSpec *contents.DefaultSpec, nonDeterministicValues *llm.NonDeterministicValues) map[string]interface{} {
	binaries := make(map[string]interface{})
	for linuxPath := range computeArtifactPaths(defaultSpec, nonDeterministicValues) {
		binaries[linuxPath+".exe"] = map[string]interface{}{}
	}
	return binaries
}

// extractArtifacts returns the global artifacts section (Linux binaries + license).
// Windows (.exe) artifacts are emitted per-target under targets.windowscross.artifacts.
func extractArtifacts(defaultSpec *contents.DefaultSpec, nonDeterministicValues *llm.NonDeterministicValues) map[string]interface{} {
	binaries := make(map[string]interface{})
	for path := range computeArtifactPaths(defaultSpec, nonDeterministicValues) {
		binaries[path] = map[string]interface{}{}
	}
	return map[string]interface{}{
		"binaries": binaries,
		"licenses": map[string]interface{}{
			defaultSpec.Repo + "/LICENSE": map[string]interface{}{},
		},
	}
}
