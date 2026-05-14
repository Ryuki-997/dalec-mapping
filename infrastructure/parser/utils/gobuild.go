package utils

import (
	"dalec-mapping/domain/contents"
	"path"
	"regexp"
	"strings"
)

// ═══════════════════════════════════════════════════════════════════════════════
// Go Build Command Parsing Utilities
//
// Shared regex patterns and functions used by both the Dockerfile parser and
// the Makefile parser to extract structured binary info from `go build` commands.
// ═══════════════════════════════════════════════════════════════════════════════

// GoBuildRe matches `go build` commands in shell/recipe lines.
var GoBuildRe = regexp.MustCompile(`go\s+build\b`)

// GoBuildOutputFlagRe captures the -o <path> argument from a go build command.
var GoBuildOutputFlagRe = regexp.MustCompile(`-o\s+(\S+)`)

// GoLdflagsRe captures the -ldflags "..." argument from a go build command.
var GoLdflagsRe = regexp.MustCompile(`-ldflags\s+["']([^"']+)["']`)

// GoLdflagsVarRe captures -ldflags ${VAR} (unquoted variable reference).
var GoLdflagsVarRe = regexp.MustCompile(`-ldflags\s+(\$\{?\w+\}?)`)

// LineContinuationRe matches shell line continuations.
var LineContinuationRe = regexp.MustCompile(`\\\s*\n\s*`)

// ParseGoBuildCommand parses a single `go build ...` command into a SpecBinary.
func ParseGoBuildCommand(cmd string) contents.SpecBinary {
	binary := contents.SpecBinary{
		BuildCommand: CleanStaticBuildCommand(cmd),
	}

	// Extract -o <path>
	if match := GoBuildOutputFlagRe.FindStringSubmatch(cmd); match != nil {
		outputPath := match[1]
		binary.OutputPath = outputPath
		binary.Name = path.Base(strings.TrimSuffix(outputPath, "${BIN_SUFFIX}"))
	}

	// Extract -ldflags "..."
	if match := GoLdflagsRe.FindStringSubmatch(cmd); match != nil {
		binary.LdFlags = match[1]
	} else if match := GoLdflagsVarRe.FindStringSubmatch(cmd); match != nil {
		binary.LdFlags = match[1]
	}

	// If no -o flag, try to derive name from the last argument (package path).
	if binary.Name == "" {
		fields := strings.Fields(cmd)
		if len(fields) > 0 {
			lastArg := fields[len(fields)-1]
			// The last argument is typically a package path like ./cmd/client/main.go
			// or ./cmd/foo or just .
			if strings.HasPrefix(lastArg, "./") || strings.HasPrefix(lastArg, "/") {
				base := path.Base(lastArg)
				if base != "." && base != "main.go" {
					binary.Name = strings.TrimSuffix(base, ".go")
				}
			}
		}
	}

	return binary
}

// CleanStaticBuildCommand strips env assignments and unnecessary prefixes.
func CleanStaticBuildCommand(cmd string) string {
	// Remove GOOS/GOARCH/CGO_ENABLED env prefixes — handled by Dalec.
	envPrefixes := []string{"GOOS=linux", "GOOS=windows", "GOARCH=amd64", "GOARCH=arm64", "CGO_ENABLED=0", "CGO_ENABLED=1"}
	for _, prefix := range envPrefixes {
		cmd = strings.ReplaceAll(cmd, prefix+" ", "")
	}
	cmd = strings.TrimSpace(cmd)
	// Remove single quotes (Dalec doesn't use them in commands).
	cmd = strings.ReplaceAll(cmd, "'", "")
	return cmd
}

// SplitShellCommands splits a shell line on && and ; delimiters.
func SplitShellCommands(shellLine string) []string {
	// Replace ; with && so we can split on one delimiter.
	shellLine = strings.ReplaceAll(shellLine, ";", "&&")
	parts := strings.Split(shellLine, "&&")
	var result []string
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			result = append(result, part)
		}
	}
	return result
}
