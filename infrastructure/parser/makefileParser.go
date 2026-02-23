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
		info.Variables[key] = value
	}
}

func GetVariable(info *contents.MakefileInfo, key string) (string, bool) {
	value, exists := info.Variables[key]
	return value, exists
}
