package transformer

import "dalec-mapping/domain/contents"

// extractBuildExtensions builds the `x-build-extensions:` map.
// It declares the image name, repository, build targets, and per-target platform
// overrides (e.g. windows/amd64 for the windowscross target).
func extractBuildExtensions(defaultSpec *contents.DefaultSpec) map[string]interface{} {
	ext := map[string]interface{}{
		"image-name":    defaultSpec.SpecImageName,
		"repository":    defaultSpec.SpecRepository,
		"build-targets": defaultSpec.BuildTargets,
	}

	for _, bt := range defaultSpec.BuildTargets {
		if string(bt) == "windowscross/container" {
			ext["per-target"] = map[string]interface{}{
				"windowscross": map[string]interface{}{
					"platforms": []string{"windows/amd64"},
				},
			}
			break
		}
	}

	return ext
}
