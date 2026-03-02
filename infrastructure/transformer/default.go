package transformer

import (
	"dalec-mapping/domain/contents"
	"fmt"
	"os"
	"strings"
)

// populateArgs builds the top-level args map. referencedVars is the set of
// variable names actually used in the build command/ldflags — only Makefile
// variables in this set are promoted to args with their resolved values.
func populateArgs(defaultSpec *contents.DefaultSpec, makefileInfo *contents.MakefileInfo, referencedVars map[string]bool) map[string]interface{} {
	args := make(map[string]interface{})
	args["REVISION"] = defaultSpec.Revision
	args["VERSION"] = defaultSpec.Version
	args["COMMIT"] = defaultSpec.LatestCommit

	selfHandledArgs := map[string]bool{
		"OS_VERSION": true,
		"VERSION":    true,
		// Docker sets these automatically during build — never include them in spec args
		"TARGETARCH": true,
		"TARGETOS":   true,
	}

	// Initialize empty MakefileInfo if nil
	if makefileInfo == nil {
		makefileInfo = &contents.MakefileInfo{
			Variables: make(map[string]string),
		}
	}

	// Setting sensible defaults to empty for generalization purposes.
	makefileInfo.Variables["ARCH"] = ""
	makefileInfo.Variables["OS"] = ""
	makefileInfo.Variables["TARGETARCH"] = ""
	makefileInfo.Variables["TARGETOS"] = ""

	// Include Dockerfile ARGs
	for k, v := range defaultSpec.Args {
		if selfHandledArgs[k] {
			continue
		}

		value := v
		if value == "" {
			value = makefileInfo.Variables[k]
		}
		value = NestedValueReplacement(defaultSpec, makefileInfo, value)

		// Skip args that resolved to empty (e.g. Docker-provided TARGETARCH/TARGETOS)
		if value == "" {
			continue
		}

		args[k] = value
		fmt.Printf("key: %s, value: %v\n", k, args[k])
	}

	// Include Makefile variables that are actually referenced in build commands
	for varName := range referencedVars {
		if selfHandledArgs[varName] {
			continue
		}
		// Skip if already populated from Dockerfile ARGs or defaults
		if _, exists := args[varName]; exists {
			continue
		}
		if rawValue, exists := makefileInfo.Variables[varName]; exists {
			resolved := NestedValueReplacement(defaultSpec, makefileInfo, rawValue)
			args[varName] = resolved
			fmt.Printf("key (from Makefile): %s, value: %v\n", varName, resolved)
		}
	}

	return args
}

func NestedValueReplacement(defaultSpec *contents.DefaultSpec, makefileInfo *contents.MakefileInfo, value string) string {

	fmt.Printf("Before: %s\n", value)

	// Make built-in functions that cannot be resolved at spec-generation time.
	// If a $(func ...) reference starts with one of these, replace it with empty.
	makeFunctions := []string{
		"shell ", "wildcard ", "patsubst ", "subst ", "strip ",
		"findstring ", "filter ", "filter-out ", "sort ", "word ",
		"wordlist ", "words ", "firstword ", "lastword ", "dir ",
		"notdir ", "suffix ", "basename ", "addsuffix ", "addprefix ",
		"join ", "realpath ", "abspath ", "if ", "or ", "and ",
		"foreach ", "call ", "eval ", "origin ", "flavor ", "value ",
		"error ", "warning ", "info ",
	}

	// Handle both $( and ${ patterns
	for {
		dollarParenIdx := strings.Index(value, "$(")
		dollarBraceIdx := strings.Index(value, "${")

		if dollarParenIdx == -1 && dollarBraceIdx == -1 {
			break
		}

		var nestedIndex int
		var startPattern, endPattern string

		if dollarParenIdx != -1 && (dollarBraceIdx == -1 || dollarParenIdx < dollarBraceIdx) {
			nestedIndex = dollarParenIdx
			startPattern = "$("
			endPattern = ")"
		} else {
			nestedIndex = dollarBraceIdx
			startPattern = "${"
			endPattern = "}"
		}

		// Skip escaped variables ($${ or $$() - literal $ in Makefiles
		if nestedIndex > 0 && value[nestedIndex-1] == '$' {
			fmt.Printf("Skipping escaped variable at index %d\n", nestedIndex)
			break
		}

		endIndex := strings.Index(value[nestedIndex:], endPattern)
		if endIndex == -1 {
			fmt.Printf("Broken makefile variable reference in value: %s\n", value)
			os.Exit(1)
		}

		nestedKey := value[nestedIndex+2 : nestedIndex+endIndex]

		// Check if this is a Make function call (e.g. "shell git rev-parse ...")
		isMakeFunc := false
		for _, fn := range makeFunctions {
			if strings.HasPrefix(nestedKey, fn) {
				isMakeFunc = true
				break
			}
		}
		if isMakeFunc {
			fmt.Printf("Skipping Make function: %s%s%s\n", startPattern, nestedKey, endPattern)
			value = value[:nestedIndex] + value[nestedIndex+endIndex+len(endPattern):]
			// Clean up path separators left after removal (e.g. "$(shell ...)/cns" → "/cns" → "cns")
			value = strings.TrimLeft(value, "/")
			value = strings.TrimSpace(value)
			continue
		}

		fmt.Printf("Nested replacement found at index: %d (pattern: %s)\n", nestedIndex, startPattern)

		nestedValue, exists := makefileInfo.Variables[nestedKey]
		if !exists {
			nestedValue, exists = defaultSpec.Args[nestedKey]
			if !exists {
				fmt.Printf("Undefined makefile variable %s referenced in value: %s\n", nestedKey, value)
				os.Exit(1)
			}
		}
		value = strings.ReplaceAll(value, startPattern+nestedKey+endPattern, nestedValue)
		fmt.Printf("Value after nested replacement: %s\n", value)
	}

	fmt.Printf("After: %s\n", value)

	return value
}

func populateMetadata(defaultSpec *contents.DefaultSpec, spec map[string]interface{}) {
	spec["name"] = strings.ToLower(defaultSpec.Repo)
	spec["packager"] = "Azure Container Upstream"
	spec["vendor"] = "Microsoft Corporation"
	spec["license"] = defaultSpec.License
	spec["website"] = defaultSpec.GitURL
	spec["description"] = defaultSpec.Description
	spec["version"] = "${VERSION}"
	spec["revision"] = "${REVISION}"
}
