package parser

import (
	"bufio"
	"os"
	"strings"
)

type MakefileInfo struct {
	Variables map[string]string
	Targets   map[string][]string
}

func ParseMakefile(filepath string) (*MakefileInfo, error) {
	file, err := os.Open(filepath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	info := &MakefileInfo{
		Variables: make(map[string]string),
		// Some other fields in the future
	}

	scanner := bufio.NewScanner(file)

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

func parseVariable(line string, info *MakefileInfo) {
	var key, value string

	if index := strings.Index(line, "="); index != -1 {
		key = strings.TrimSpace(line[:index])
		value = strings.TrimSpace(line[index+1:])
	} else if index := strings.Index(line, ":="); index != -1 {
		key = strings.TrimSpace(line[:index])
		value = strings.TrimSpace(line[index+2:])
	} else if index := strings.Index(line, "?="); index != -1 {
		key = strings.TrimSpace(line[:index])
		value = strings.TrimSpace(line[index+2:])
	}

	if key != "" {
		value = strings.TrimSpace(value)
		info.Variables[key] = value
	}
}

func GetVariable(info *MakefileInfo, key string) (string, bool) {
	value, exists := info.Variables[key]
	return value, exists
}
