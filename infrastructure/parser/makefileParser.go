package parser

import (
	"bufio"
	"dalec-mapping/domain/contents"
	"regexp"
	"strings"
)

// makefileGoBuildRe matches `go build ... <target>` in Makefile recipe lines.
// Captures the last argument (package target) which is a path like cmd/client/main.go or ./cmd/client.
var makefileGoBuildRe = regexp.MustCompile(`go\s+build\s+.+?\s+((?:\./)?[a-zA-Z][^\s]*)\s*$`)

// shellBuiltins lists shell commands that can trail a go build in compound
// lines (e.g. "go build ./cmd/foo && popd") and must not be treated as targets.
var shellBuiltins = map[string]bool{
	"popd": true, "pushd": true, "cd": true, "exit": true,
	"echo": true, "return": true, "break": true, "continue": true,
}

func ParseMakefile(makefile []byte, info *contents.MakefileInfo) (*contents.MakefileInfo, error) {
	scanner := bufio.NewScanner(strings.NewReader(string(makefile)))

	for scanner.Scan() {
		rawLine := scanner.Text()
		line := strings.TrimSpace(rawLine)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		if strings.Contains(line, "=") && !strings.HasPrefix(rawLine, "\t") {
			parseVariable(line, info)
			continue
		}

		// Extract go build package targets from recipe lines.
		if strings.HasPrefix(rawLine, "\t") && strings.Contains(line, "go build") {
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
