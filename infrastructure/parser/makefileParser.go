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
			if m := makefileGoBuildRe.FindStringSubmatch(line); m != nil {
				target := m[1]
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
