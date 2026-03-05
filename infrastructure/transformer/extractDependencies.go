package transformer

// extractDependencies returns an empty global dependencies section.
// All build and runtime dependencies are managed per-target in extractTargets.
// Placing any dep here would apply it to ALL targets including windowscross, which
// Dalec rejects for runtime deps on Windows output images.
func extractDependencies() map[string]interface{} {
	return map[string]interface{}{
		"build":   map[string]interface{}{},
		"runtime": map[string]interface{}{},
	}
}