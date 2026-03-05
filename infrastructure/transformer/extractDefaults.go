package transformer

import (
	"dalec-mapping/domain/contents"
	"fmt"
	"os"
	"strings"
)

// selfHandledArgs lists variables that are emitted with fixed logic and must not
// be overridden by Dockerfile ARG values or Makefile variable promotion.
var selfHandledArgs = map[string]bool{
	"OS_VERSION": true,
	"VERSION":    true,
	"TARGETARCH": true,
	"TARGETOS":   true,
	"GOOS":       true,
	"GOARCH":     true,
}

// makeFunctions lists Make built-in function names that cannot be resolved at
// spec-generation time and should be stripped during variable expansion.
var makeFunctions = []string{
	"shell ", "wildcard ", "patsubst ", "subst ", "strip ",
	"findstring ", "filter ", "filter-out ", "sort ", "word ",
	"wordlist ", "words ", "firstword ", "lastword ", "dir ",
	"notdir ", "suffix ", "basename ", "addsuffix ", "addprefix ",
	"join ", "realpath ", "abspath ", "if ", "or ", "and ",
	"foreach ", "call ", "eval ", "origin ", "flavor ", "value ",
	"error ", "warning ", "info ",
}

// populateMetadata writes the fixed metadata fields into the spec map.
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

// populateArgs builds the top-level args map.
// referencedVars is the set of variable names actually used in build commands/ldflags;
// only Makefile variables in this set are promoted to args with their resolved values.
func populateArgs(defaultSpec *contents.DefaultSpec, makefileInfo *contents.MakefileInfo, referencedVars map[string]bool) map[string]interface{} {
	args := map[string]interface{}{
		"REVISION":   defaultSpec.Revision,
		"VERSION":    defaultSpec.Version,
		"COMMIT":     defaultSpec.LatestCommit,
		// Emitted as blank — BuildKit injects actual values at build time.
		"TARGETOS":   "",
		"TARGETARCH": "",
	}

	makefileInfo = effectiveMakefileInfo(makefileInfo)
	args = mergeDockerfileArgs(args, defaultSpec, makefileInfo)
	args = mergeMakefileVars(args, makefileInfo, referencedVars, defaultSpec)
	return args
}

// effectiveMakefileInfo returns a non-nil MakefileInfo, seeding platform
// variables to empty so callers never see unresolved ${ARCH}/${OS} references.
func effectiveMakefileInfo(makefileInfo *contents.MakefileInfo) *contents.MakefileInfo {
	if makefileInfo == nil {
		makefileInfo = &contents.MakefileInfo{Variables: make(map[string]string)}
	}
	makefileInfo.Variables["ARCH"] = ""
	makefileInfo.Variables["OS"] = ""
	makefileInfo.Variables["TARGETARCH"] = ""
	makefileInfo.Variables["TARGETOS"] = ""
	return makefileInfo
}

// mergeDockerfileArgs folds Dockerfile ARG values into args, resolving any
// nested Makefile variable references. Empty-after-resolution values are dropped.
func mergeDockerfileArgs(args map[string]interface{}, defaultSpec *contents.DefaultSpec, makefileInfo *contents.MakefileInfo) map[string]interface{} {
	for k, v := range defaultSpec.Args {
		if selfHandledArgs[k] {
			continue
		}
		value := v
		if value == "" {
			value = makefileInfo.Variables[k]
		}
		value = NestedValueReplacement(defaultSpec, makefileInfo, value)
		if value == "" {
			continue
		}
		args[k] = value
		fmt.Printf("key: %s, value: %v\n", k, value)
	}
	return args
}

// mergeMakefileVars promotes Makefile variables that are actually referenced in
// build commands into args, resolving their values.
func mergeMakefileVars(args map[string]interface{}, makefileInfo *contents.MakefileInfo, referencedVars map[string]bool, defaultSpec *contents.DefaultSpec) map[string]interface{} {
	for varName := range referencedVars {
		if selfHandledArgs[varName] {
			continue
		}
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

// NestedValueReplacement expands all $(VAR) and ${VAR} references in value using
// makefileInfo and defaultSpec.Args. Make built-in function calls are stripped.
// Exits on unresolvable variable references or malformed syntax.
func NestedValueReplacement(defaultSpec *contents.DefaultSpec, makefileInfo *contents.MakefileInfo, value string) string {
	fmt.Printf("Before: %s\n", value)

	for {
		start, startPat, endPat := nextVarRef(value)
		if start == -1 {
			break
		}

		// Skip escaped variables ($${ or $$()
		if start > 0 && value[start-1] == '$' {
			fmt.Printf("Skipping escaped variable at index %d\n", start)
			break
		}

		endOffset := strings.Index(value[start:], endPat)
		if endOffset == -1 {
			fmt.Printf("Broken makefile variable reference in value: %s\n", value)
			os.Exit(1)
		}

		key := value[start+2 : start+endOffset]

		if isMakeFunction(key) {
			fmt.Printf("Skipping Make function: %s%s%s\n", startPat, key, endPat)
			value = value[:start] + value[start+endOffset+len(endPat):]
			value = strings.TrimLeft(value, "/")
			value = strings.TrimSpace(value)
			continue
		}

		fmt.Printf("Nested replacement found at index: %d (pattern: %s)\n", start, startPat)

		replacement, ok := resolveVarRef(key, makefileInfo, defaultSpec)
		if !ok {
			fmt.Printf("Undefined makefile variable %s referenced in value: %s\n", key, value)
			os.Exit(1)
		}
		value = strings.ReplaceAll(value, startPat+key+endPat, replacement)
		fmt.Printf("Value after nested replacement: %s\n", value)
	}

	fmt.Printf("After: %s\n", value)
	return value
}

// nextVarRef finds the earliest $( or ${ in value.
// Returns (index, startPattern, endPattern) or (-1,"","") if none found.
func nextVarRef(value string) (int, string, string) {
	pi := strings.Index(value, "$(")
	bi := strings.Index(value, "${")
	switch {
	case pi == -1 && bi == -1:
		return -1, "", ""
	case pi != -1 && (bi == -1 || pi < bi):
		return pi, "$(", ")"
	default:
		return bi, "${", "}"
	}
}

// isMakeFunction reports whether key begins with a known Make built-in function name.
func isMakeFunction(key string) bool {
	for _, fn := range makeFunctions {
		if strings.HasPrefix(key, fn) {
			return true
		}
	}
	return false
}

// resolveVarRef looks up key first in makefileInfo.Variables, then in defaultSpec.Args.
func resolveVarRef(key string, makefileInfo *contents.MakefileInfo, defaultSpec *contents.DefaultSpec) (string, bool) {
	if v, ok := makefileInfo.Variables[key]; ok {
		return v, true
	}
	if v, ok := defaultSpec.Args[key]; ok {
		return v, true
	}
	return "", false
}

