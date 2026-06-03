package parser

import (
	"bufio"
	"path"
	"regexp"
	"strings"

	"dalec-mapping/domain/contents"
)

// makefileGoBuildRe matches `go build ... <target>` in Makefile recipe lines.
// Captures the last argument (package target) which is a path like cmd/client/main.go or ./cmd/client.
var makefileGoBuildRe = regexp.MustCompile(`go\s+build\s+.+?\s+((?:\./)?[a-zA-Z][^\s]*)\s*$`)

// makefileVarRe matches Makefile variable references: $(VAR) or ${VAR}.
var makefileVarRe = regexp.MustCompile(`\$\(([A-Za-z_][A-Za-z0-9_]*)\)`)

// makefileTargetRe matches Makefile target lines (e.g. "build-azure-cns:" or "test-aks-node-controller:").
var makefileTargetRe = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_.%-]*`)

// shellBuiltins lists shell commands that can trail a go build in compound
// lines (e.g. "go build ./cmd/foo && popd") and must not be treated as targets.
var shellBuiltins = map[string]bool{
	"popd": true, "pushd": true, "cd": true, "exit": true,
	"echo": true, "return": true, "break": true, "continue": true,
}

// targetRecipe pairs a Makefile target name with a go build recipe line found under it.
type targetRecipe struct {
	target string
	line   string
}

// ParseMakefile extracts variables and go build commands from a Makefile.
// When imageName is non-empty, a cascading match strategy is applied to select
// which go build commands to extract:
//  1. Targets whose name contains imageName (e.g. "build-azure-cns" for imageName="azure-cns")
//  2. Targets whose name contains "build" (e.g. generic "build:" target)
//  3. All targets (fallback — accept any go build command)
//
// When imageName is empty, all go build commands are extracted (backward compat).
func ParseMakefile(makefile []byte, imageName string) (contents.MakefileInfo, error) {
	info := contents.MakefileInfo{
		Variables: make(map[string]string),
	}
	recipes := collectRecipes(makefile, &info)
	matchedRecipes := selectRecipes(recipes, imageName)
	extractBuildCommands(matchedRecipes, &info)
	return info, nil
}

// collectRecipes scans the Makefile once, extracting variables into info and
// collecting all (target, goBuildLine) pairs for later filtering.
func collectRecipes(makefile []byte, info *contents.MakefileInfo) []targetRecipe {
	scanner := bufio.NewScanner(strings.NewReader(string(makefile)))
	currentTarget := ""
	var recipes []targetRecipe

	for scanner.Scan() {
		rawLine := scanner.Text()
		line := strings.TrimSpace(rawLine)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Detect Makefile target lines (non-indented, contains ":" but not "=").
		if !strings.HasPrefix(rawLine, "\t") && strings.Contains(line, ":") && !strings.Contains(line, "=") {
			if m := makefileTargetRe.FindString(line); m != "" {
				currentTarget = m
			}
			continue
		}

		if strings.Contains(line, "=") && !strings.HasPrefix(rawLine, "\t") {
			parseVariable(line, info)
			continue
		}

		// Collect go build recipe lines with their parent target.
		if strings.HasPrefix(rawLine, "\t") && strings.Contains(line, "go build") {
			recipes = append(recipes, targetRecipe{target: currentTarget, line: line})
		}
	}
	return recipes
}

// selectRecipes applies the cascading match strategy against collected recipes.
// Returns the subset matching the first successful filter level.
func selectRecipes(recipes []targetRecipe, imageName string) []targetRecipe {
	if imageName == "" {
		return recipes
	}

	// Level 1: targets containing the spec image name.
	bySpecImageName := filterRecipes(recipes, func(target string) bool {
		return strings.Contains(target, imageName)
	})
	if len(bySpecImageName) > 0 {
		return bySpecImageName
	}

	// Level 2: targets containing "build".
	byBuildKeyword := filterRecipes(recipes, func(target string) bool {
		return strings.Contains(target, "build")
	})
	if len(byBuildKeyword) > 0 {
		return byBuildKeyword
	}

	// Level 3: accept all targets (default fallback).
	return recipes
}

// filterRecipes returns recipes whose target satisfies the predicate.
func filterRecipes(recipes []targetRecipe, matches func(string) bool) []targetRecipe {
	var result []targetRecipe
	for _, recipe := range recipes {
		if matches(recipe.target) {
			result = append(result, recipe)
		}
	}
	return result
}

// extractBuildCommands processes matched recipe lines, populating GoBuildTargets
// and GoBuildCommands on the MakefileInfo.
func extractBuildCommands(recipes []targetRecipe, info *contents.MakefileInfo) {
	for _, recipe := range recipes {
		// Isolate the go build command from compound shell lines
		// (e.g. "pushd dir && go build ./cmd/foo && popd").
		goBuildSegment := recipe.line
		if idx := strings.Index(recipe.line, "go build"); idx >= 0 {
			goBuildSegment = recipe.line[idx:]
		}
		// Strip trailing shell commands chained after the build.
		for _, sep := range []string{" && ", " || ", " ; "} {
			if i := strings.Index(goBuildSegment, sep); i > 0 {
				goBuildSegment = goBuildSegment[:i]
			}
		}
		if m := makefileGoBuildRe.FindStringSubmatch(goBuildSegment); m != nil {
			target := m[1]
			if !shellBuiltins[target] {
				// Normalize file paths (e.g. cmd/client/main.go → ./cmd/client)
				if strings.HasSuffix(target, ".go") {
					idx := strings.LastIndex(target, "/")
					if idx > 0 {
						target = "./" + strings.TrimPrefix(target[:idx], "./")
					}
				}
				if !strings.HasPrefix(target, "./") {
					target = "./" + target
				}
				info.GoBuildTargets = append(info.GoBuildTargets, target)
			}
		}

		// Parse the full go build command for Name, OutputPath, LdFlags.
		// Only include commands with -o flag (actual binary builds, not compile checks like ./...).
		normalizedSegment := convertMakefileVarsToShell(goBuildSegment)
		resolvedSegment := resolveMakefileVars(normalizedSegment, info.Variables)
		resolvedSegment = convertMakefileVarsToShell(resolvedSegment)
		binary := ParseGoBuildCommand(resolvedSegment)
		if binary.Name != "" && binary.OutputPath != "" {
			originalOutputPath := binary.OutputPath
			binary.OutputPath = normalizeBinaryOutputPath(binary.OutputPath, binary.Name)
			if binary.BuildCommand != "" && originalOutputPath != binary.OutputPath {
				binary.BuildCommand = strings.Replace(binary.BuildCommand, originalOutputPath, binary.OutputPath, 1)
			}
			replaced := false
			for i, existing := range info.GoBuildCommands {
				if existing.Name == binary.Name {
					info.GoBuildCommands[i] = binary
					replaced = true
					break
				}
			}
			if !replaced {
				info.GoBuildCommands = append(info.GoBuildCommands, binary)
			}
		}
	}
}

func parseVariable(line string, info *contents.MakefileInfo) {
	var key, value string

	if index := strings.Index(line, ":="); index != -1 {
		key = strings.TrimSpace(line[:index])
		value = strings.TrimSpace(line[index+2:])
	} else if index := strings.Index(line, "?="); index != -1 {
		key = strings.TrimSpace(line[:index])
		value = strings.TrimSpace(line[index+2:])
	} else if index := strings.Index(line, "="); index != -1 {
		key = strings.TrimSpace(line[:index])
		value = strings.TrimSpace(line[index+1:])
	}

	if key != "" {
		value = strings.TrimSpace(value)
		// Strip surrounding quotes: '"value"' → 'value', '""' → ''
		if len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"' {
			value = value[1 : len(value)-1]
		}
		info.Variables[key] = value
	}
}

// convertMakefileVarsToShell converts Makefile $(VAR) references to shell ${VAR} syntax.
func convertMakefileVarsToShell(command string) string {
	return makefileVarRe.ReplaceAllString(command, "${$1}")
}

// resolveMakefileVars expands ${VAR} references using known Makefile variables.
// Unresolved variables are left as ${VAR} for promotion to spec args.
func resolveMakefileVars(command string, variables map[string]string) string {
	for varName, varValue := range variables {
		command = strings.ReplaceAll(command, "${"+varName+"}", varValue)
	}
	return command
}

// normalizeBinaryOutputPath rewrites the output path to Dalec convention /go/bin/<name>.
// If the path is relative or non-standard, it uses the binary name to build the standard path.
func normalizeBinaryOutputPath(outputPath, binaryName string) string {
	if outputPath == "" {
		return "/go/bin/" + binaryName
	}
	// If already an absolute path under /go/bin/, keep it.
	if strings.HasPrefix(outputPath, "/go/bin/") {
		return outputPath
	}
	// Use the base name from the parsed output path to build the Dalec standard path.
	baseName := path.Base(outputPath)
	if baseName == "." || baseName == "/" {
		baseName = binaryName
	}
	return "/go/bin/" + baseName
}
