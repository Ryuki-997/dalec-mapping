package transformer

import "dalec-mapping/domain/contents"

// extractBuildExtensions builds the `x-build-extensions:` map.
// It declares the image name, repository, build targets, and per-target platform
// overrides (e.g. windows/amd64 for the windowscross target).
func extractBuildExtensions(defaultSpec *contents.DefaultSpec) map[string]interface{} {
	ext := map[string]interface{}{
		"image-name":    defaultSpec.SpecImageName,
		"build-targets": defaultSpec.BuildTargets,
	}

	if defaultSpec.SpecRepository != "" {
		ext["repository"] = defaultSpec.SpecRepository
	}

	for _, bt := range defaultSpec.BuildTargets {
		if bt.IsWindows() {
			ext["per-target"] = map[string]interface{}{
				bt.OS(): map[string]interface{}{
					"platforms": []string{bt.Platform()},
				},
			}
			break
		}
	}

	return ext
}
