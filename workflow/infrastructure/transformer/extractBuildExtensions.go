package transformer

import "dalec-mapping/domain/workplan"

// extractBuildExtensions builds the `x-build-extensions:` map.
// It declares the image name, repository, build targets, and per-target platform
// overrides (e.g. windows/amd64 for the windowscross target).
func extractBuildExtensions(item *workplan.WorkItem) map[string]interface{} {
	subject := item.Naming
	buildTargets := onboardBuildTargets(item)

	ext := map[string]interface{}{
		"build-targets": buildTargets,
	}

	if subject.SpecRepository != "" {
		ext["repository"] = subject.SpecRepository
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
