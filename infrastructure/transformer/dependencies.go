package transformer

import (
	"dalec-mapping/domain/contents"
	"dalec-mapping/domain/llm"
	"dalec-mapping/domain/repository"
)

// extractDependencies extracts build and runtime dependencies (uses nonDeterministicValues if available)
func extractDependencies(defaultSpec *contents.DefaultSpec, nonDeterministicValues *llm.NonDeterministicValues) map[string]interface{} {
	deps := make(map[string]interface{})
	buildDeps := make(map[string]interface{})
	runtimeDeps := make(map[string]interface{})

	// Use agent-extracted values if available
	if nonDeterministicValues != nil && len(nonDeterministicValues.BuildDeps) > 0 {
		for _, dep := range nonDeterministicValues.BuildDeps {
			buildDeps[dep] = map[string]interface{}{}
		}
	}

	if nonDeterministicValues != nil && len(nonDeterministicValues.RuntimeDeps) > 0 {
		for _, dep := range nonDeterministicValues.RuntimeDeps {
			runtimeDeps[dep] = map[string]interface{}{}
		}
	}

	// Always add language-specific build dependencies based on the source generator
	if defaultSpec != nil && defaultSpec.Generator == repository.GoModGenerator {
		buildDeps["msft-golang"] = map[string]interface{}{}
	}

	// Ensure build & runtime dependencies are included
	deps["build"] = buildDeps
	deps["runtime"] = runtimeDeps

	return deps
}