package transformer

import "dalec-mapping/domain/contents"

// extractSources builds the `sources:` map for the Dalec spec.
// It always emits a single git source with a go-module generator.
// When the repo URL includes a subdirectory, a `subpath` field is added to the
// generator entry so Dalec runs the module fetch from the correct sub-tree.
func extractSources(defaultSpec *contents.DefaultSpec) map[string]interface{} {
	// Generator entry: { gomod: {} } plus optional subpath for monorepo subdirs.
	generatorEntry := map[string]interface{}{
		string(defaultSpec.Generator): map[string]interface{}{},
	}
	if defaultSpec.Subdir != "" {
		generatorEntry["subpath"] = defaultSpec.Subdir
	}

	return map[string]interface{}{
		defaultSpec.Repo: map[string]interface{}{
			"git": map[string]interface{}{
				"url":    defaultSpec.GitURL,
				"commit": "${COMMIT}",
			},
			"generate": []map[string]interface{}{generatorEntry},
		},
	}
}
