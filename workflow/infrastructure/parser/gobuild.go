package parser

import (
	"path"
	"regexp"
	"strings"

	"dalec-mapping/domain/contents"
)

// ═══════════════════════════════════════════════════════════════════════════════
// Go Build Command Parsing Utilities
//
// Shared regex patterns and functions used by both the Dockerfile parser and
// the Makefile parser to extract structured binary info from `go build` commands.
// ═══════════════════════════════════════════════════════════════════════════════

// goBuildRe matches `go build` commands in shell/recipe lines.
var goBuildRe = regexp.MustCompile(`go\s+build\b`)

// goBuildOutputFlagRe captures the -o <path> argument from a go build command.
var goBuildOutputFlagRe = regexp.MustCompile(`-o\s+(\S+)`)

// goLdflagsRe captures the -ldflags "..." argument from a go build command.
var goLdflagsRe = regexp.MustCompile(`-ldflags\s+["']([^"']+)["']`)

// goLdflagsVarRe captures -ldflags ${VAR} (unquoted variable reference).
var goLdflagsVarRe = regexp.MustCompile(`-ldflags\s+(\$\{?\w+\}?)`)

// lineContinuationRe matches shell line continuations.
var lineContinuationRe = regexp.MustCompile(`\\\s*\n\s*`)

// parseGoBuildCommand parses a single `go build ...` command into a SpecBinary.
func parseGoBuildCommand(cmd string) contents.SpecBinary {
	binary := contents.SpecBinary{
		BuildCommand: cleanStaticBuildCommand(cmd),
	}

	// Extract -o <path>
	if match := goBuildOutputFlagRe.FindStringSubmatch(cmd); match != nil {
		outputPath := match[1]
		binary.OutputPath = outputPath
		binary.Name = path.Base(strings.TrimSuffix(outputPath, "${BIN_SUFFIX}"))
	}

	// Extract -ldflags "..."
	if match := goLdflagsRe.FindStringSubmatch(cmd); match != nil {
		binary.LdFlags = match[1]
	} else if match := goLdflagsVarRe.FindStringSubmatch(cmd); match != nil {
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

// cleanStaticBuildCommand strips env assignments and unnecessary prefixes.
func cleanStaticBuildCommand(cmd string) string {
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

// splitShellCommands splits a shell line on && and ; delimiters.
func splitShellCommands(shellLine string) []string {
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
