package transformer

// ═══════════════════════════════════════════════════════════════════════════════
// extractBuild.go — Generates the `build:` section (env + steps) of a Dalec spec.
//
//   Chunk 1 · ORCHESTRATION        extractBuildSection()
//     Assembles env + steps into the build map, then scans for ${VAR} refs
//     so the caller can promote them to top-level args.
//     Calls → buildEnv(), buildSteps(), scanVarReferences()
//
//   Chunk 2 · ENVIRONMENT           buildEnv()
//     Static Go env vars (GOPROXY, CGO_ENABLED, etc.) plus LDFLAGS when present.
//
//   Chunk 3 · COMMAND ASSEMBLY      buildSteps()
//     Merges per-binary build commands and pipeline steps into one shell script.
//     Order: preamble → normal binary builds → pipeline steps → deferred sub-module builds.
//     Calls → rawBuildCommands(), extractCdDir(), rewriteGoModCdPaths(),
//             injectAllBinSuffixVars(), binSuffixPreamble()
//
//   Chunk 4 · PER-BINARY PROCESSING rawBuildCommands()
//     Cleans each binary's build command (inlines ldflags, strips env assignments),
//     injects ${BIN_SUFFIX}, optionally renames the -o target to match the entrypoint.
//     Calls → cleanBuildCommand(), injectBinSuffixVar(), isSubmoduleName(),
//             stripGoModDownloadPrefix()
//
//   Chunk 5 · UTILITIES
//     binSuffixPreamble()       — shell preamble setting BIN_SUFFIX + CC
//     injectBinSuffixVar()      — appends ${BIN_SUFFIX} to first -o /go/bin/<name>
//     injectAllBinSuffixVars()  — same, all occurrences (for pipeline steps)
//     scanVarReferences()       — finds ${VAR} refs in commands for arg promotion
//     extractOutputFlag()       — extracts -o path from a go build command
//     extractCdDir()            — splits "cd X && rest" into (X, rest)
//     stripGoModDownloadPrefix()— removes go mod download prefix handled as source
//     rewriteGoModCdPaths()     — /go/pkg/mod/… → "$BUILD_ROOT"/<sourceKey>
//     isSubmoduleName()         — checks if name matches a go-mod-download source
//     cleanBuildCommand()       — inlines ldflags, strips env assignments, cleans whitespace
// ═══════════════════════════════════════════════════════════════════════════════

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"dalec-mapping/domain/contents"
	"dalec-mapping/domain/llm"
)

// cdDirRe matches `cd <dir> && <rest>` or `cd <dir>\n<rest>` patterns.
var cdDirRe = regexp.MustCompile(`(?s)^cd\s+(\S+)\s*(?:&&|\n)\s*(.+)$`)

// binOutRe matches -o /go/bin/<name> in build commands (no variable refs in the name portion).
var binOutRe = regexp.MustCompile(`-o (/go/bin/[^${}\s]+)`)

// ─── Chunk 1 · ORCHESTRATION ─────────────────────────────────────────────────

// extractBuildSection assembles the top-level `build:` map for a Dalec spec.
// Returns the build map and the set of ${VAR} names referenced inside it,
// so the caller can forward them as top-level args.
func extractBuildSection(defaultSpec *contents.DefaultSpec, makefileInfo *contents.MakefileInfo, nonDeterministicValues *llm.NonDeterministicValues, goModDownloads []GoModDownloadInfo) (map[string]interface{}, map[string]bool) {
	build := make(map[string]interface{})

	env := buildEnv()
	build["env"] = env

	steps, scanText := buildSteps(defaultSpec, nonDeterministicValues, goModDownloads)
	build["steps"] = steps

	referencedVars := scanVarReferences(scanText)

	// Promote any referenced Makefile variable into env so the spec arg is wired through.
	// Skip variables that are set dynamically in the build preamble (e.g. OS, ARCH)
	// or already present in env.
	for varName := range referencedVars {
		if _, alreadySet := env[varName]; alreadySet {
			continue
		}
		if dalecHandledEnvs[varName] {
			continue
		}
		if makefileInfo != nil {
			if _, exists := makefileInfo.Variables[varName]; exists {
				env[varName] = fmt.Sprintf("${%s}", varName)
			}
		}
	}

	return build, referencedVars
}

// ─── Chunk 2 · ENVIRONMENT ───────────────────────────────────────────────────

// buildEnv constructs the env map for the build section.
// Standard Go build vars are always included. Per-binary ldflags are embedded
// directly in each binary's build command — no global LDFLAGS env entry.
func buildEnv() map[string]interface{} {
	return map[string]interface{}{
		"GOPROXY":      "direct",
		"GOEXPERIMENT": "systemcrypto",
		"CGO_ENABLED": "1", // required by GOEXPERIMENT=systemcrypto (FIPS)
		"VERSION":     "${VERSION}",
		"GOOS":        "${TARGETOS}",
		"GOARCH":      "${TARGETARCH}",
	}
}

// ─── Chunk 3 · COMMAND ASSEMBLY ──────────────────────────────────────────────

// buildSteps converts NonDeterministicValues binaries into a single Dalec `steps` entry.
// All binaries are merged into one command block so BIN_SUFFIX and CC are set once.
// Also returns the combined command text for var scanning.
// baseDir is always the repo source name (the root of the cloned source). The LLM-provided
// build command's `cd` paths are always relative to the repo root, not the Dockerfile subdir.
func buildSteps(defaultSpec *contents.DefaultSpec, nonDeterministicValues *llm.NonDeterministicValues, goModDownloads []GoModDownloadInfo) ([]map[string]interface{}, string) {
	baseDir := defaultSpec.Repo

	rawCmds := rawBuildCommands(nonDeterministicValues, goModDownloads)

	// Fallback: no commands extracted — emit a minimal go build step.
	if len(rawCmds) == 0 {
		fallback := fmt.Sprintf("cd %s\ngo build -o /go/bin/%s ./main.go", baseDir, defaultSpec.Repo)
		return []map[string]interface{}{{"command": fallback}}, fallback
	}

	// For each raw command, resolve the cd path and the pure build line.
	type buildLine struct {
		cdPath      string // full cd path (baseDir or baseDir/subdir)
		cmd         string // the go build command (without cd prefix)
		isSubmodule bool   // true for go-mod-download sub-module builds (deferred after pipeline)
	}

	var buildLines []buildLine
	for _, raw := range rawCmds {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		subdir, rest := extractCdDir(raw)
		// Normalize: strip leading '/' — the LLM may emit absolute paths from
		// the Dockerfile's WORKDIR but Dalec's sandbox uses relative paths.
		subdir = strings.TrimPrefix(subdir, "/")

		// Binaries whose cd path points into go/pkg/mod/ are built from a
		// separate Go module (e.g. dropgz). Rewrite to use the DALEC source
		// key (relative to the build dir).
		if strings.HasPrefix(subdir, "go/pkg/mod/") {
			rewritten := rewriteGoModCdPaths("cd /"+subdir+" && "+rest, goModDownloads)
			newSubdir, newRest := extractCdDir(rewritten)
			newSubdir = strings.TrimPrefix(newSubdir, "/")
			buildLines = append(buildLines, buildLine{cdPath: newSubdir, cmd: newRest, isSubmodule: true})
			continue
		}

		cdPath := baseDir
		if subdir != "" && subdir != baseDir {
			// Keep the subdirectory cd as part of the command itself so the
			// working directory stays at baseDir for subsequent pipeline steps.
			rest = fmt.Sprintf("cd %s && %s && cd ..", subdir, rest)
		}
		buildLines = append(buildLines, buildLine{cdPath: cdPath, cmd: rest})
	}

	if len(buildLines) == 0 {
		fallback := fmt.Sprintf("cd %s\ngo build -o /go/bin/%s ./main.go", baseDir, defaultSpec.Repo)
		return []map[string]interface{}{{"command": fallback}}, fallback
	}

	// Build the merged command block: preamble once, then cd + build lines.
	var parts []string
	parts = append(parts, binSuffixPreamble())

	// Split buildLines into normal binaries and deferred sub-module builds.
	// Sub-module builds (e.g. dropgz) depend on pipeline steps that prepare
	// embedded content (/payload), so they must run AFTER pipeline steps.
	var normalLines, deferredLines []buildLine
	for _, bl := range buildLines {
		if bl.isSubmodule {
			deferredLines = append(deferredLines, bl)
		} else {
			normalLines = append(normalLines, bl)
		}
	}

	// Emit normal binary builds. Only emit cd when the path changes.
	// ${BIN_SUFFIX} is NOT injected here — a single final-pass injection
	// targets the very last `-o /go/bin/<name>` in the entire assembled
	// command block (which may come from pipeline steps or deferred lines).
	lastCd := ""
	for _, bl := range normalLines {
		if bl.cdPath != lastCd {
			parts = append(parts, "cd "+bl.cdPath)
			lastCd = bl.cdPath
		}
		parts = append(parts, bl.cmd)
	}

	// Append pipeline steps (intermediate + wrapper stages) after the primary builds.
	// Pipeline steps handle their own directory navigation, so no automatic cd-back
	// is inserted here.
	if nonDeterministicValues != nil && len(nonDeterministicValues.PipelineSteps) > 0 {
		// Ensure directories from intermediate-stage WORKDIRs exist. The Dockerfile
		// creates these implicitly via WORKDIR, but Dalec's single-command sandbox
		// does not. Only emit mkdir for dirs the LLM hasn't already handled.
		if mkdirs := stageWorkdirs(defaultSpec.Stages, nonDeterministicValues.PipelineSteps, baseDir); len(mkdirs) > 0 {
			parts = append(parts, "mkdir -p "+strings.Join(mkdirs, " "))
		}
		for _, step := range nonDeterministicValues.PipelineSteps {
			step = strings.TrimSpace(step)
			if step == "" {
				continue
			}
			// Skip `go mod download` steps — these are handled as DALEC sources.
			if goModDownloadRe.MatchString(step) {
				continue
			}
			// Rewrite `cd /go/pkg/mod/<module>@<version>` to `cd "$BUILD_ROOT"/<sourceKey>`.
			step = rewriteGoModCdPaths(step, goModDownloads)
			// Rewrite bare `cd <sourceKey>` to `cd "$BUILD_ROOT"/<sourceKey>` so
			// the path resolves correctly after an absolute cd (e.g. cd /payload).
			step = rewriteRelativeSourceCd(step, goModDownloads)
			parts = append(parts, step)
		}
	}

	// Emit deferred sub-module binary builds AFTER pipeline steps.
	for _, bl := range deferredLines {
		parts = append(parts, "cd "+bl.cdPath)
		parts = append(parts, bl.cmd)
	}

	// Final pass: inject ${BIN_SUFFIX} on only the LAST `-o /go/bin/<name>`
	// across the entire assembled command block — regardless of whether it
	// came from a normal line, pipeline step, or deferred line.
	stepCmd := strings.Join(parts, "\n")
	stepCmd = injectLastBinSuffix(stepCmd)
	return []map[string]interface{}{{"command": stepCmd}}, stepCmd
}

// ─── Chunk 4 · PER-BINARY PROCESSING ─────────────────────────────────────────

// rawBuildCommands cleans each binary's fields and returns one command string per binary.
// Output is always /go/bin/<canonicalName>${BIN_SUFFIX} — the canonical name is derived
// from the primary linux entrypoint when it differs from the LLM binary name (e.g.
// "dropgz" when binaries[0].Name is "azure-ipam"). BIN_SUFFIX is injected so the same
// step works for both Linux (BIN_SUFFIX="") and windowscross (BIN_SUFFIX=".exe").
func rawBuildCommands(nonDeterministicValues *llm.NonDeterministicValues, goModDownloads []GoModDownloadInfo) []string {
	if nonDeterministicValues == nil {
		return nil
	}

	epBase := entrypointBinaryName(nonDeterministicValues)

	var cmds []string

	for i := range nonDeterministicValues.Binaries {
		aux := &nonDeterministicValues.Binaries[i]
		if aux.Name == "" {
			continue
		}

		aux.BuildCommand = cleanBuildCommand(aux.BuildCommand, aux.LdFlags)
		aux.LdFlags = strings.Trim(aux.LdFlags, `"'`)

		var cmd string
		if aux.BuildCommand != "" {
			cmd = stripGoModDownloadPrefix(aux.BuildCommand)
		} else if aux.LdFlags != "" {
			// No explicit build command — synthesise one from ldflags + output path.
			out := "/go/bin/" + aux.Name
			cmd = fmt.Sprintf("go build -ldflags \"%s\" -o %s", aux.LdFlags, out)
		}

		// When there is exactly ONE binary and the entrypoint reveals a different canonical
		// name (e.g. the build should produce "dropgz" but the LLM recorded "azure-ipam"),
		// rename the -o output path so it matches the declared artifacts.binaries entry.
		// For multi-binary specs each binary keeps its own name.
		// Skip the rename when the canonical name matches a sub-module source (e.g. "dropgz")
		// — in that case the sub-module is built separately via pipeline steps, and the
		// binary listed here is the real intermediate build that should keep its original name.
		if cmd != "" && epBase != "" && epBase != aux.Name && len(nonDeterministicValues.Binaries) == 1 && !isSubmoduleName(epBase, goModDownloads) {
			cmd = strings.ReplaceAll(cmd,
				"/go/bin/"+aux.Name+"${BIN_SUFFIX}",
				"/go/bin/"+epBase+"${BIN_SUFFIX}",
			)
		}

		if cmd != "" {
			cmds = append(cmds, cmd)
			fmt.Printf("Build step: %v\n", cmd)
		}
	}
	return cmds
}

// ─── Chunk 5 · UTILITIES ─────────────────────────────────────────────────────

// binSuffixPreamble returns the shell preamble that sets BIN_SUFFIX and, when
// GOOS=windows, exports CC pointing to the MinGW cross-compiler.
func binSuffixPreamble() string {
	return `BUILD_ROOT="$PWD"
BIN_SUFFIX=""
OS="linux"
if [ "${GOOS}" = "windows" ]; then
  BIN_SUFFIX=".exe"
  OS="windows"
  export CC=` + MingwBinDir + `/x86_64-w64-mingw32-clang
fi`
}

// injectBinSuffixVar rewrites the FIRST `-o /go/bin/<name>` → `-o /go/bin/<name>${BIN_SUFFIX}`
// in a build command. Does NOT add the preamble — that is emitted once by buildSteps.
func injectBinSuffixVar(cmd string) string {
	loc := binOutRe.FindStringSubmatchIndex(cmd)
	if loc == nil {
		return cmd
	}
	return cmd[:loc[3]] + "${BIN_SUFFIX}" + cmd[loc[3]:]
}

// injectLastBinSuffix rewrites only the LAST `-o /go/bin/<name>` occurrence
// in a multi-line command block, appending `${BIN_SUFFIX}` to the output path.
// This ensures the suffix targets the final deliverable binary regardless of
// whether it comes from a normal build line, pipeline step, or deferred line.
func injectLastBinSuffix(text string) string {
	allLocs := binOutRe.FindAllStringSubmatchIndex(text, -1)
	if len(allLocs) == 0 {
		return text
	}
	last := allLocs[len(allLocs)-1]
	return text[:last[3]] + "${BIN_SUFFIX}" + text[last[3]:]
}

// injectAllBinSuffixVars rewrites ALL `-o /go/bin/<name>` occurrences in a
// (possibly multi-line) string. Used for pipeline steps that may contain
// multiple go build commands.
func injectAllBinSuffixVars(text string) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		lines[i] = injectBinSuffixVar(line)
	}
	return strings.Join(lines, "\n")
}

// scanVarReferences finds all ${VAR}/$(VAR) references in the merged command text.
func scanVarReferences(cmdText string) map[string]bool {
	varRefRe := regexp.MustCompile(`\$[{(]([A-Za-z_][A-Za-z0-9_]*)[})]`)
	refs := make(map[string]bool)

	for _, m := range varRefRe.FindAllStringSubmatch(cmdText, -1) {
		refs[m[1]] = true
	}
	return refs
}

// extractOutputFlag extracts the path passed to -o in a go build command.
func extractOutputFlag(cmd string) string {
	re := regexp.MustCompile(`\s-o\s+(\S+)`)
	if m := re.FindStringSubmatch(cmd); m != nil {
		return m[1]
	}
	return ""
}

// extractCdDir parses a command of the form "cd X && <rest>" or "cd X\n<rest>".
// Returns (X, rest) when matched, or ("", original line) otherwise.
func extractCdDir(line string) (subdir, stripped string) {
	line = strings.TrimSpace(line)
	if m := cdDirRe.FindStringSubmatch(line); m != nil {
		return m[1], strings.TrimSpace(m[2])
	}
	return "", line
}

// stripGoModDownloadPrefix removes a leading `go mod download <module>@<version> && `
// from a build command. The download is handled as a DALEC source, not at build time.
func stripGoModDownloadPrefix(cmd string) string {
	loc := goModDownloadRe.FindStringIndex(cmd)
	if loc == nil {
		return cmd
	}
	rest := cmd[loc[1]:]
	if strings.HasPrefix(rest, " && ") {
		return strings.TrimSpace(rest[4:])
	}
	return cmd
}

// rewriteGoModCdPaths replaces `cd /go/pkg/mod/<module>@<version>` with
// `cd "$BUILD_ROOT"/<sourceKey>`. BUILD_ROOT is set in the preamble to the
// initial working directory (where DALEC extracts sources).
func rewriteGoModCdPaths(step string, downloads []GoModDownloadInfo) string {
	for _, dl := range downloads {
		// Match patterns like:
		//   cd /go/pkg/mod/github.com/azure/azure-container-networking/dropgz@${DROPGZ_VERSION}
		//   cd /go/pkg/mod/github.com/azure/azure-container-networking/dropgz@v0.0.12
		goModPath := "/go/pkg/mod/" + dl.ModulePath + "@" + dl.VersionVar
		step = strings.ReplaceAll(step, goModPath, `"$BUILD_ROOT"/`+dl.SourceKey)
	}
	return step
}

// rewriteRelativeSourceCd rewrites a leading `cd <sourceKey>` to
// `cd "$BUILD_ROOT"/<sourceKey>` when <sourceKey> matches a known
// go-mod-download source. This ensures the path resolves correctly
// even after an earlier absolute cd has changed the working directory.
func rewriteRelativeSourceCd(step string, downloads []GoModDownloadInfo) string {
	for _, dl := range downloads {
		prefix := "cd " + dl.SourceKey
		if step == prefix ||
			strings.HasPrefix(step, prefix+" ") ||
			strings.HasPrefix(step, prefix+"\t") ||
			strings.HasPrefix(step, prefix+"&&") {
			step = `cd "$BUILD_ROOT"/` + dl.SourceKey + step[len(prefix):]
		}
	}
	return step
}

// isSubmoduleName returns true if name matches a detected go-mod-download sub-module
// source key. Used to avoid renaming the primary binary when the entrypoint comes
// from a sub-module that is built separately.
func isSubmoduleName(name string, downloads []GoModDownloadInfo) bool {
	for _, dl := range downloads {
		if dl.SourceKey == name {
			return true
		}
	}
	return false
}

// dalecHandledEnvs lists env vars that Dalec sets natively and must be stripped
// from LLM-emitted build commands.
var dalecHandledEnvs = map[string]bool{
	"CGO_ENABLED": true, "GOOS": true, "GOARCH": true,
	"GOARM": true, "GOARM64": true, "OS": true, "ARCH": true,
	"CC": true, // set globally via MinGW toolchain source
}

// standardWorkdirs lists working directories that are always available in the
// build sandbox and never need an explicit mkdir.
var standardWorkdirs = map[string]bool{
	"/":       true,
	"/go":     true,
	"/go/src": true,
	"/go/bin": true,
}

// stageWorkdirs collects non-standard WORKDIR paths from Dockerfile stages that
// need to be created before pipeline steps run. It deduplicates against dirs the
// LLM already `mkdir -p`'d in pipelineSteps and excludes standard build paths.
func stageWorkdirs(stages []contents.Stage, pipelineSteps []string, baseDir string) []string {
	// Collect all WORKDIRs from stages.
	candidates := map[string]bool{}
	for _, stage := range stages {
		wd := strings.TrimSpace(stage.Workdir)
		if wd == "" || !strings.HasPrefix(wd, "/") {
			continue
		}
		if standardWorkdirs[wd] {
			continue
		}
		// Go module cache paths are dependency dirs populated by sources, not build dirs.
		if strings.HasPrefix(wd, "/go/pkg/mod/") {
			continue
		}
		// The repo source dir (and any subdirectory within it) is already
		// present in the mounted source — no mkdir needed.
		if wd == "/"+baseDir || strings.HasPrefix(wd, "/"+baseDir+"/") {
			continue
		}
		candidates[wd] = true
	}

	if len(candidates) == 0 {
		return nil
	}

	// Exclude dirs the LLM already handles via mkdir -p in pipeline steps.
	for _, step := range pipelineSteps {
		step = strings.TrimSpace(step)
		if strings.HasPrefix(step, "mkdir") {
			// Extract paths from "mkdir -p /path1 /path2 ..." or "mkdir -p rel ..."
			fields := strings.Fields(step)
			for _, f := range fields {
				if f == "mkdir" || f == "-p" || f == "-m" {
					continue
				}
				if strings.HasPrefix(f, "/") {
					delete(candidates, f)
				} else {
					// LLM may use relative paths; match against absolute candidates.
					delete(candidates, "/"+f)
				}
			}
		}
	}

	// Drop any remaining WORKDIR that no pipeline step actually references.
	// Dockerfile WORKDIRs like /azure-ipam may correspond to repo subdirectories
	// that exist in the source tree — pipeline steps use them via relative paths
	// (e.g. azure-ipam/*.conflist) and never need the absolute directory.
	for wd := range candidates {
		referenced := false
		for _, step := range pipelineSteps {
			step = strings.TrimSpace(step)
			if strings.HasPrefix(step, "mkdir") {
				continue
			}
			if strings.Contains(step, wd) {
				referenced = true
				break
			}
		}
		if !referenced {
			delete(candidates, wd)
		}
	}

	dirs := make([]string, 0, len(candidates))
	for d := range candidates {
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)
	return dirs
}

// envAssignRe matches KEY=value, KEY=${VAR}, KEY=${VAR:-default}, KEY=$(VAR).
var envAssignRe = regexp.MustCompile(`(\w+)=(?:\$[\{\(][^\}\)]*[\}\)]|\S*)`)

// cleanBuildCommand prepares a raw build command for the Dalec spec:
//  1. Inlines ldflags — replaces ${LDFLAGS} with the cleaned ldflags string.
//  2. Strips inner quotes around $VAR / ${VAR} references that break shell parsing
//     when nested inside -ldflags "..." (e.g. -X main.ver="$V" → -X main.ver=${V}).
//  3. Normalises bare $VAR to ${VAR} for consistency.
//  4. Strips env assignments handled by Dalec (CGO_ENABLED, GOOS, etc.).
//  5. Cleans stray braces and whitespace.
func cleanBuildCommand(cmd, ldflags string) string {
	if cmd == "" {
		return ""
	}

	// 1. Inline ldflags.
	cleanedLd := strings.Trim(ldflags, `"'`)
	cmd = strings.ReplaceAll(cmd, "${LDFLAGS}", cleanedLd)

	// 2. Strip inner quotes wrapping $VAR / ${VAR} references.
	//    e.g. "$VERSION" → ${VERSION}, "$CNS_AI_PATH"="$CNS_AI_ID" → ${CNS_AI_PATH}=${CNS_AI_ID}
	innerQuotedVar := regexp.MustCompile(`"(\$\{?\w+\}?)"`)
	cmd = innerQuotedVar.ReplaceAllString(cmd, "$1")

	// 3. Normalise bare $VAR to ${VAR} (skip ${ which is already braced).
	bareVar := regexp.MustCompile(`\$([A-Za-z_]\w*)`)
	cmd = bareVar.ReplaceAllString(cmd, "${$1}")

	// 4. Strip Dalec-handled env assignments.
	cmd = envAssignRe.ReplaceAllStringFunc(cmd, func(match string) string {
		if eqIdx := strings.Index(match, "="); eqIdx > 0 {
			if dalecHandledEnvs[match[:eqIdx]] {
				return ""
			}
		}
		return match
	})

	// 5. Protect valid ${...} refs, remove stray braces, restore.
	validVarRefRe := regexp.MustCompile(`\$\{[^}]+\}`)
	var placeholders []string
	cmd = validVarRefRe.ReplaceAllStringFunc(cmd, func(m string) string {
		key := fmt.Sprintf("__VR%d__", len(placeholders))
		placeholders = append(placeholders, m)
		return key
	})
	cmd = strings.NewReplacer("{", "", "}", "").Replace(cmd)
	for i, val := range placeholders {
		cmd = strings.ReplaceAll(cmd, fmt.Sprintf("__VR%d__", i), val)
	}

	// 6. Collapse whitespace and double slashes.
	cmd = regexp.MustCompile(`\s{2,}`).ReplaceAllString(cmd, " ")
	cmd = regexp.MustCompile(`/{2,}`).ReplaceAllString(cmd, "/")
	return strings.TrimSpace(cmd)
}

