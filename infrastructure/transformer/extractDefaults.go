package transformer

// ═══════════════════════════════════════════════════════════════════════════════
// extractDefaults.go — Generates top-level metadata + args for a Dalec spec.
//
//   Chunk 1 · ORCHESTRATION          extractDefaultsSection()
//     Writes metadata into the spec map and returns the resolved args map.
//     Calls → extractMetadata(), extractArgs()
//
//   Chunk 2 · METADATA               extractMetadata()
//     Fixed fields: name, packager, vendor, license, website, description,
//     version, revision.
//
//   Chunk 3 · ARGS ASSEMBLY          extractArgs()
//     Builds the top-level args map from defaults, Dockerfile ARGs, and
//     Makefile variables that are actually referenced in build commands.
//     Calls → effectiveMakefileInfo(), mergeDockerfileArgs(), mergeMakefileVars()
//
//   Chunk 4 · VARIABLE RESOLUTION    expandVarRefs()
//     Iteratively expands $(VAR)/${VAR} references using Makefile + Dockerfile values.
//     Strips Make built-in function calls. Exits on unresolvable references.
//     Decomposes into: parseNextVarRef() → stripMakeFuncCall() | substituteVar()
//     Helpers: findVarRefStart(), isMakeFunction(), resolveVarRef()
// ═══════════════════════════════════════════════════════════════════════════════

import (
	"dalec-mapping/domain/contents"
	"fmt"
	"os"
	"strings"
)

// selfHandledArgs lists variables that are emitted with fixed logic and must not
// be overridden by Dockerfile ARG values or Makefile variable promotion.
var selfHandledArgs = map[string]bool{
	"OS":         true,
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

// ─── Chunk 1 · ORCHESTRATION ─────────────────────────────────────────────────

// extractDefaultsSection writes metadata into the spec and returns the resolved args map.
func extractDefaultsSection(defaultSpec *contents.DefaultSpec, makefileInfo *contents.MakefileInfo, referencedVars map[string]bool, goModDownloads []GoModDownloadInfo, spec map[string]interface{}) map[string]interface{} {
	extractMetadata(defaultSpec, spec)
	args := extractArgs(defaultSpec, makefileInfo, referencedVars, goModDownloads)
	return args
}

// ─── Chunk 2 · METADATA ─────────────────────────────────────────────────────

// extractMetadata writes the fixed metadata fields into the spec map.
func extractMetadata(defaultSpec *contents.DefaultSpec, spec map[string]interface{}) {
	spec["name"] = strings.ToLower(defaultSpec.Repo)
	spec["packager"] = "Azure Container Upstream"
	spec["vendor"] = "Microsoft Corporation"
	spec["license"] = defaultSpec.License
	spec["website"] = defaultSpec.GitURL
	spec["description"] = defaultSpec.Description
	spec["version"] = "${VERSION}"
	spec["revision"] = "${REVISION}"
}

// ─── Chunk 3 · ARGS ASSEMBLY ─────────────────────────────────────────────────

// extractArgs builds the top-level args map.
// referencedVars is the set of variable names actually used in build commands/ldflags;
// only Makefile variables in this set are promoted to args with their resolved values.
func extractArgs(defaultSpec *contents.DefaultSpec, makefileInfo *contents.MakefileInfo, referencedVars map[string]bool, goModDownloads []GoModDownloadInfo) map[string]interface{} {
	args := map[string]interface{}{
		"REVISION":   defaultSpec.Revision,
		"VERSION":    defaultSpec.Version,
		"COMMIT":     defaultSpec.LatestCommit,
		// Emitted as blank — BuildKit injects actual values at build time.
		"TARGETOS":   "",
		"TARGETARCH": "",
	}

	makefileInfo = initializeMakefileInfo(makefileInfo)
	args = mergeDockerfileArgs(args, defaultSpec, makefileInfo)
	args = mergeMakefileVars(args, makefileInfo, referencedVars, defaultSpec)
	args = mergeSubmoduleVars(args, defaultSpec, makefileInfo, goModDownloads)

	return args
}

// initializeMakefileInfo returns a non-nil MakefileInfo, seeding platform
// variables to empty so callers never see unresolved ${ARCH}/${OS} references.
func initializeMakefileInfo(makefileInfo *contents.MakefileInfo) *contents.MakefileInfo {
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
		value = expandVarRefs(defaultSpec, makefileInfo, value)
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
			resolved := expandVarRefs(defaultSpec, makefileInfo, rawValue)
			args[varName] = resolved
			fmt.Printf("key (from Makefile): %s, value: %v\n", varName, resolved)
		}
	}
	return args
}

// mergeSubmoduleVars promotes version variables referenced by detected
// go mod download submodules. These are "used" variables — their presence
// in a source commit field means they must appear in args.
// Resolves from Dockerfile ARGs first, then Makefile variables.
func mergeSubmoduleVars(args map[string]interface{}, defaultSpec *contents.DefaultSpec, makefileInfo *contents.MakefileInfo, goModDownloads []GoModDownloadInfo) map[string]interface{} {
	for _, dl := range goModDownloads {
		varName := strings.Trim(dl.VersionVar, "${}()")
		if varName == "" {
			continue
		}
		if _, exists := args[varName]; exists {
			continue
		}
		// Resolve from Dockerfile ARGs first, then Makefile variables.
		value, found := defaultSpec.Args[varName]
		if !found {
			value, found = makefileInfo.Variables[varName]
		}
		if !found {
			continue
		}
		value = expandVarRefs(defaultSpec, makefileInfo, value)
		args[varName] = value
		fmt.Printf("key (from submodule): %s, value: %v\n", varName, value)
	}
	return args
}

// ─── Chunk 4 · VARIABLE RESOLUTION ───────────────────────────────────────────

// varRef describes a single $(VAR) or ${VAR} reference found in a string.
type varRef struct {
	pos      int    // index of the leading '$'
	key      string // variable name between delimiters
	openTok  string // "$(" or "${"
	closeTok string // ")" or "}"
	span     int    // total length of the reference including delimiters
	escaped  bool   // true when preceded by another '$' (e.g. $${VAR})
}

// expandVarRefs iteratively expands all $(VAR)/${VAR} references in value
// using Makefile variables and Dockerfile ARGs. Make built-in function calls
// (e.g. $(shell ...)) are stripped rather than expanded.
func expandVarRefs(defaultSpec *contents.DefaultSpec, makefileInfo *contents.MakefileInfo, value string) string {
	fmt.Printf("Before: %s\n", value)

	for {
		ref, ok := parseNextVarRef(value)
		if !ok || ref.escaped {
			break
		}

		if isMakeFunction(ref.key) {
			value = stripMakeFuncCall(value, ref)
			continue
		}

		value = substituteVar(value, ref, makefileInfo, defaultSpec)
	}

	fmt.Printf("After: %s\n", value)
	return value
}

// parseNextVarRef finds the earliest $( or ${ reference in value, extracts the
// key name, and returns a populated varRef. Returns (_, false) when no reference
// exists. Exits on malformed syntax (missing closing delimiter).
func parseNextVarRef(value string) (varRef, bool) {
	pos, openTok, closeTok := findVarRefStart(value)
	if pos == -1 {
		return varRef{}, false
	}

	if pos > 0 && value[pos-1] == '$' {
		return varRef{escaped: true}, true
	}

	endOff := strings.Index(value[pos:], closeTok)
	if endOff == -1 {
		fmt.Printf("Broken makefile variable reference in value: %s\n", value)
		os.Exit(1)
	}

	key := value[pos+2 : pos+endOff]
	return varRef{
		pos:      pos,
		key:      key,
		openTok:  openTok,
		closeTok: closeTok,
		span:     endOff + len(closeTok),
	}, true
}

// stripMakeFuncCall removes a Make built-in function call (e.g. $(shell ...))
// from value and trims any resulting leading slashes or whitespace.
func stripMakeFuncCall(value string, ref varRef) string {
	fmt.Printf("Skipping Make function: %s%s%s\n", ref.openTok, ref.key, ref.closeTok)
	value = value[:ref.pos] + value[ref.pos+ref.span:]
	value = strings.TrimLeft(value, "/")
	return strings.TrimSpace(value)
}

// substituteVar replaces every occurrence of the variable reference in value
// with its resolved value from Makefile variables or Dockerfile ARGs.
// Exits if the variable cannot be resolved.
func substituteVar(value string, ref varRef, makefileInfo *contents.MakefileInfo, defaultSpec *contents.DefaultSpec) string {
	fmt.Printf("Nested replacement found at index: %d (pattern: %s)\n", ref.pos, ref.openTok)

	replacement, ok := resolveVarRef(ref.key, makefileInfo, defaultSpec)
	if !ok {
		fmt.Printf("Undefined makefile variable %s referenced in value: %s\n", ref.key, value)
		os.Exit(1)
	}

	value = strings.ReplaceAll(value, ref.openTok+ref.key+ref.closeTok, replacement)
	fmt.Printf("Value after nested replacement: %s\n", value)
	return value
}

// findVarRefStart finds the earliest $( or ${ in value.
// Returns (index, openToken, closeToken) or (-1, "", "") if none found.
func findVarRefStart(value string) (int, string, string) {
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

