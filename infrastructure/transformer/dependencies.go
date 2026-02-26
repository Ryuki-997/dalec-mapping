package transformer

import (
	"dalec-mapping/domain/llm"
)

// extractDependencies extracts build and runtime dependencies (uses nonDeterministicValues if available)
func extractDependencies(nonDeterministicValues *llm.NonDeterministicValues) map[string]interface{} {
	deps := make(map[string]interface{})
	buildDeps := make(map[string]interface{})
	runtimeDeps := make(map[string]interface{})

	// Per-target build deps that should NOT appear in the global section.
	// These are managed per-target in extractTargets instead.
	perTargetBuildDeps := map[string]bool{
		"msft-golang": true,
		"gcc":         true,
	}

	// Use agent-extracted values if available
	if nonDeterministicValues != nil && len(nonDeterministicValues.BuildDeps) > 0 {
		for _, dep := range nonDeterministicValues.BuildDeps {
			if perTargetBuildDeps[dep] {
				continue
			}
			buildDeps[dep] = map[string]interface{}{}
		}
	}

	if nonDeterministicValues != nil && len(nonDeterministicValues.RuntimeDeps) > 0 {
		for _, dep := range nonDeterministicValues.RuntimeDeps {
			runtimeDeps[dep] = map[string]interface{}{}
		}
	}

	// Ensure build & runtime dependencies are included
	deps["build"] = buildDeps
	deps["runtime"] = runtimeDeps

	return deps
}