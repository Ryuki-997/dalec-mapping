package transformer

import (
	"dalec-mapping/pipeline"
	"strings"
)

// extractBuildExtensions builds the `x-build-extensions:` map.
// It declares the image name, repository, build targets, and per-target platform
// overrides (e.g. windows/amd64 for the windowscross target).
func extractBuildExtensions() map[string]interface{} {
	onboard := pipeline.Current.Onboard
	repoInfo := pipeline.Current.RepoInfo
	buildTargets := onboardBuildTargets()

	ext := map[string]interface{}{
		"build-targets": buildTargets,
	}

	// Emit image-name when it differs from the spec name (repo name).
	// This maps the spec to a specific image within the repository
	// (e.g. "azure-cni" within the "azure-container-networking" repo).
	imageName := strings.ToLower(onboard.SpecImageName)
	if imageName != strings.ToLower(repoInfo.Repo) {
		ext["image-name"] = imageName
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
