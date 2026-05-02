package transformer

import (
	"dalec-mapping/pipeline"
)

// extractBuildExtensions builds the `x-build-extensions:` map.
// It declares the image name, repository, build targets, and per-target platform
// overrides (e.g. windows/amd64 for the windowscross target).
func extractBuildExtensions() map[string]interface{} {
	onboard := pipeline.Current.Onboard
	buildTargets := onboardBuildTargets()

	ext := map[string]interface{}{
		"image-name":    onboard.SpecImageName,
		"build-targets": buildTargets,
	}

	if onboard.SpecRepository != "" {
		ext["repository"] = onboard.SpecRepository
	}

	for _, bt := range buildTargets {
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
