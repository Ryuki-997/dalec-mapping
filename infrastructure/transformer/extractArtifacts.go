package transformer

import (
	"dalec-mapping/domain/contents"
	"dalec-mapping/domain/llm"
	"dalec-mapping/infrastructure/github"

	"fmt"
	"path/filepath"
)

// computeArtifactPaths returns the Linux binary artifact paths (no .exe).
// When the primary linux entrypoint names a different binary than binaries[0]
// (e.g. the build wraps "azure-ipam" into a "dropgz" container binary), the
// artifact path is rewritten to /go/bin/<entrypointBase> so it matches the
// file actually produced by the build step.
func computeArtifactPaths(defaultSpec *contents.DefaultSpec, nonDeterministicValues *llm.NonDeterministicValues) map[string]interface{} {
	paths := make(map[string]interface{})

	// Canonical binary name derived from the symlinks key (the real installed binary path).
	// This is the map KEY in image.post.symlinks, which is the target path that artifacts
	// get installed to (e.g. "/usr/bin/dropgz"). The entrypoint is where the symlink lives.
	epBase := ""
	if nonDeterministicValues != nil {
		if lt := findPrimaryLinuxTarget(nonDeterministicValues.Targets); lt != nil {
			epBase = canonicalBase(lt.Symlink)
		}
	}

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

			// Override when the entrypoint reveals a different canonical name
			// (e.g. "dropgz" when outputPath ends in "azure-ipam").
			if epBase != "" && canonicalBase(outputPath) != epBase {
				outputPath = "/go/bin/" + epBase
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
