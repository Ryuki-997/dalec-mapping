package transformer

// extractSources creates source definitions from Dockerfile
func extractSources(defaultSpec *DefaultSpec) map[string]interface{} {
	sources := make(map[string]interface{})
	sourceName := defaultSpec.Repo

	// Main git source - always included
	mainSource := make(map[string]interface{})
	git := make(map[string]interface{})
	git["url"] = defaultSpec.GitURL
	git["commit"] = "${COMMIT}"
	mainSource["git"] = git
	mainSource["generate"] = []map[string]interface{}{
		{string(defaultSpec.Generator): map[string]interface{}{}},
	}
	sources[sourceName] = mainSource

	// 	// Iterate through all stages to find additional sources
	// 	for _, stage := range defaultSpec.Stages {
	// 		for _, inst := range stage.Instructions {
	// 			switch inst.Type {
	// 			case "ADD":
	// 				// Check if it's HTTP source
	// 				if len(inst.Args) <= 0 || !isHTTPURL(inst.Args[0]) {
	// 					continue
	// 				}
	// 				src := extractHTTPSource(inst)
	// 				if src == nil {
	// 					continue
	// 				}
	// 				srcName := generateSourceName(inst.Args[0])
	// 				sources[srcName] = src

	// 			case "COPY":
	// 				// Check if copying from external image
	// 				if fromRef, ok := inst.Flags["from"]; ok {
	// 					if isExternalImage(fromRef, defaultSpec.Stages) {
	// 						src := extractImageSource(inst, fromRef)
	// 						if src != nil {
	// 							srcName := generateSourceName(fromRef)
	// 							sources[srcName] = src
	// 						}
	// 					}
	// 				}

	// 			case "RUN":
	// 				// Check for HTTP downloads in RUN commands
	// 				if len(inst.Args) > 0 {
	// 					cmd := strings.Join(inst.Args, " ")
	// 					if src := extractHTTPFromRun(cmd); src != nil {
	// 						srcName := generateSourceName(src["url"].(string))
	// 						sources[srcName] = src
	// 					}
	// 				}
	// 			}
	// 		}
	// 	}

	return sources
}

// // ///////////////////////////////////////////////
// // /// Helper functions for source extraction  ///
// // ///////////////////////////////////////////////

// func isHTTPURL(s string) bool {
// 	return strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://")
// }

// // extractHTTPSource creates an HTTP source from ADD instruction
// func extractHTTPSource(inst parser.RawInstruction) map[string]interface{} {
// 	if len(inst.Args) < 2 {
// 		return nil
// 	}

// 	sources := inst.Args[:len(inst.Args)-1]
// 	destination := inst.Args[len(inst.Args)-1]

// 	httpSources := []string{}
// 	for _, src := range sources {
// 		if isHTTPURL(src) {
// 			httpSources = append(httpSources, src)
// 		}
// 	}

// 	if len(httpSources) == 0 {
// 		return nil
// 	}

// 	// Construct dalec source
// 	source := make(map[string]interface{})
// 	http := make(map[string]interface{})
// 	http["url"] = inst.Args[0]
// 	source["http"] = http

// 	// Destination path if provided
// 	if len(inst.Args) > 1 {
// 		source["path"] = inst.Args[1]
// 	}

// 	return source
// }

// // extractImageSource creates an image source from COPY --from=external
// func extractImageSource(inst parser.RawInstruction, imageRef string) map[string]interface{} {
// 	source := make(map[string]interface{})
// 	image := make(map[string]interface{})
// 	image["ref"] = imageRef
// 	source["image"] = image

// 	// Add includes if source paths specified
// 	if len(inst.Args) > 0 {
// 		includes := inst.Args[:len(inst.Args)-1] // All but destination
// 		if len(includes) > 0 {
// 			source["includes"] = includes
// 		}
// 	}

// 	// Destination path
// 	if len(inst.Args) > 0 {
// 		source["path"] = inst.Args[len(inst.Args)-1]
// 	}

// 	return source
// }

// // extractHTTPFromRun extracts HTTP downloads from RUN commands
// func extractHTTPFromRun(cmd string) map[string]interface{} {
// 	// Check for curl/wget patterns
// 	if strings.Contains(cmd, "curl") || strings.Contains(cmd, "wget") {
// 		// Simple URL extraction - look for http(s):// patterns
// 		words := strings.Fields(cmd)
// 		for _, word := range words {
// 			if isHTTPURL(word) {
// 				source := make(map[string]interface{})
// 				http := make(map[string]interface{})
// 				http["url"] = word
// 				source["http"] = http
// 				return source
// 			}
// 		}
// 	}
// 	return nil
// }

// // isExternalImage checks if a reference is an external image vs internal stage
// func isExternalImage(ref string, stages []parser.Stage) bool {
// 	// Check if it matches any stage name
// 	for _, stage := range stages {
// 		if stage.Name == ref {
// 			return false // It's an internal stage
// 		}
// 	}
// 	// Not a stage name, must be external
// 	return true
// }

// // generateSourceName creates a source name from URL or image ref
// func generateSourceName(ref string) string {
// 	// Extract meaningful name from URL or image reference
// 	ref = strings.TrimPrefix(ref, "http://")
// 	ref = strings.TrimPrefix(ref, "https://")

// 	// Remove file extension and special chars
// 	ref = strings.Split(ref, "/")[len(strings.Split(ref, "/"))-1]
// 	ref = strings.Split(ref, "?")[0]
// 	ref = strings.TrimSuffix(ref, ".tar.gz")
// 	ref = strings.TrimSuffix(ref, ".zip")
// 	ref = strings.TrimSuffix(ref, ".tgz")

// 	// Clean up for YAML key
// 	ref = strings.ReplaceAll(ref, ":", "-")
// 	ref = strings.ReplaceAll(ref, "@", "-")

// 	return ref
// }
