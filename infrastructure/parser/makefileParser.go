package parser

import (
	"bufio"
	"dalec-mapping/domain/contents"
	parserutils "dalec-mapping/infrastructure/parser/utils"
	"path"
	"regexp"
	"strings"
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

// ParseMakefile extracts variables and go build commands from a Makefile.
// When imageName is non-empty, only go build commands under targets whose name
// contains imageName are extracted (e.g. imageName="azure-cns" matches target "test-azure-cns").
// When imageName is empty, all go build commands are extracted (backward compat).
func ParseMakefile(makefile []byte, info *contents.MakefileInfo, imageName string) (*contents.MakefileInfo, error) {
	scanner := bufio.NewScanner(strings.NewReader(string(makefile)))
	currentTarget := ""

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

		// Extract go build package targets from recipe lines.
		if strings.HasPrefix(rawLine, "\t") && strings.Contains(line, "go build") {
			// When imageName is set, only extract from targets whose name contains it.
			if imageName != "" && !strings.Contains(currentTarget, imageName) {
				continue
			}
			// Isolate the go build command from compound shell lines
			// (e.g. "pushd dir && go build ./cmd/foo && popd").
			goBuildSegment := line
			if idx := strings.Index(line, "go build"); idx >= 0 {
				goBuildSegment = line[idx:]
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
			binary := parserutils.ParseGoBuildCommand(resolvedSegment)
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
	return info, nil
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
