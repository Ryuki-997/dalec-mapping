package parser

import (
	"bufio"
	"dalec-mapping/domain/contents"
	"strings"
)

func ParseMakefile(makefile []byte, info *contents.MakefileInfo) (*contents.MakefileInfo, error) {
	scanner := bufio.NewScanner(strings.NewReader(string(makefile)))

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		if strings.Contains(line, "=") && !strings.HasPrefix(line, "\t") {
			parseVariable(line, info)
			continue
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
