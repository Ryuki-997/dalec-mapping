package transformer

// ═══════════════════════════════════════════════════════════════════════════════
// buildUtils.go — Utility functions used during Dockerfile build-step generation.
//
//   DECLARATIONS            regex patterns, env/workdir maps, shell preamble
//
//   Chunk 1 · STRING REWRITERS
//     cleanBuildCommand()          — inlines ldflags, strips env/quotes, normalises vars
//     stripDalecHandledEnvs()      — strips leading GOOS/CGO_ENABLED/etc. env prefixes
//     stripGoModDownloadPrefix()   — removes go mod download prefix handled as source
//     injectArtifactBinSuffix()    — appends ${BIN_SUFFIX} to artifact -o paths
//
//   Chunk 2 · SUBMODULE TARGETING
//     rewriteSubmoduleBuildCd()    — returns (cdPath, step) for submodule go builds
//     rewriteGoModPath()           — rewrites go/pkg/mod/… path → "$BUILD_ROOT"/<sourceKey>
//     rewriteGoModCdPaths()        — /go/pkg/mod/… → "$BUILD_ROOT"/<sourceKey> in full commands
//     rewriteRelativeSourceCd()    — bare cd <sourceKey> → cd "$BUILD_ROOT"/<sourceKey>
//
//   Chunk 3 · COMMAND PARSING
//     extractCdDir()               — splits "cd X && rest" into (X, rest)
//
//   Chunk 4 · STAGE ANALYSIS
//     submoduleStageCopies()       — COPY --from from Go builder stages
//     intermediateStageCopies()    — COPY --from from non-Go intermediate stages
//     stageWorkdirs()              — non-standard WORKDIRs needing mkdir
// ═══════════════════════════════════════════════════════════════════════════════

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"dalec-mapping/domain/contents"
	"dalec-mapping/domain/workplan"
	"dalec-mapping/workflow/infrastructure/parser"
)

// ═══════════════════════════════════════════════════════════════════════════════
// DECLARATIONS — regex patterns, constant maps, shell preamble
// ═══════════════════════════════════════════════════════════════════════════════

// --- Regex: variable references ---

// bareVarRe matches bare $VAR references with uppercase names (dalec args),
// skipping lowercase shell variables like $f in for-loops.
var bareVarRe = regexp.MustCompile(`\$([A-Z_][A-Z0-9_]*)`)

// varRefRe matches ${VAR} or $(VAR) references for scanning referenced args.
var varRefRe = regexp.MustCompile(`\$[{(]([A-Za-z_][A-Za-z0-9_]*)[})]`)

// parenVarRe matches $(VAR) Makefile-style variable references for conversion to ${VAR}.
var parenVarRe = regexp.MustCompile(`\$\(([A-Za-z_][A-Za-z0-9_]*)\)`)

// validVarRefRe matches all ${...} variable references (for brace-stripping protection).
var validVarRefRe = regexp.MustCompile(`\$\{[^}]+\}`)

// innerQuotedVarRe matches double-quoted $VAR or ${VAR} references.
var innerQuotedVarRe = regexp.MustCompile(`"(\$\{?\w+\}?)"`) //nolint:gocritic

// --- Regex: command structure ---

// cdDirRe matches `cd <dir> && <rest>` or `cd <dir>\n<rest>` patterns.
var cdDirRe = regexp.MustCompile(`(?s)^cd\s+(\S+)\s*(?:&&|\n)\s*(.+)$`)

// outputFlagRe matches -o <path> in go build commands.
var outputFlagRe = regexp.MustCompile(`\s-o\s+(\S+)`)

// binOutRe matches -o /go/bin/<name> in build commands (no variable refs in the name portion).
var binOutRe = regexp.MustCompile(`-o (/go/bin/[^${}\s]+)`)

// --- Regex: whitespace/formatting ---

// collapseSpacesRe matches two or more consecutive whitespace characters.
var collapseSpacesRe = regexp.MustCompile(`\s{2,}`)

// doubleSlashRe matches two or more consecutive forward slashes.
var doubleSlashRe = regexp.MustCompile(`/{2,}`)

// --- Constant maps ---

// dalecHandledEnvs lists env vars that Dalec sets natively and must be stripped
// from parsed build commands.
var dalecHandledEnvs = map[string]bool{
	"CGO_ENABLED": true, "GOOS": true, "GOARCH": true,
	"GOARM": true, "GOARM64": true, "OS": true, "ARCH": true,
	"GO111MODULE": true, "GOEXPERIMENT": true,
}

// standardWorkdirs lists working directories that are always available in the
// build sandbox and never need an explicit mkdir.
var standardWorkdirs = map[string]bool{
	"/":       true,
	"/go":     true,
	"/go/src": true,
	"/go/bin": true,
}

// --- Shell preamble ---

// binSuffixPreamble is the shell preamble that sets BIN_SUFFIX and BUILD_ROOT.
var binSuffixPreamble = `BUILD_ROOT="$PWD"
BIN_SUFFIX=""
OS="linux"
if [ "${GOOS}" = "windows" ]; then
  BIN_SUFFIX=".exe"
  OS="windows"
fi`

// ─── Chunk 1 · STRING REWRITERS ──────────────────────────────────────────────

// cleanBuildCommand prepares a raw build command for the Dalec spec:
//  1. Inlines ldflags — replaces ${LDFLAGS} with the cleaned ldflags string.
//  2. Strips quotes (single quotes entirely, inner double quotes around $VAR refs).
//  3. Converts Makefile-style $(VAR) to shell-style ${VAR}.
//  4. Normalises bare $VAR to ${VAR} for consistency.
//  5. Strips leading env assignments handled by Dalec (CGO_ENABLED, GOOS, etc.).
//  6. Strips stray braces (preserving valid ${...} refs).
//  7. Collapses whitespace and double slashes.
func cleanBuildCommand(cmd, ldflags string) string {
	if cmd == "" {
		return ""
	}

	// 1. Inline ldflags.
	cleanedLd := strings.Trim(ldflags, `"'`)
	cmd = strings.ReplaceAll(cmd, "${LDFLAGS}", `"`+cleanedLd+`"`)

	// 2. Strip quotes.
	cmd = strings.ReplaceAll(cmd, "'", "")
	cmd = innerQuotedVarRe.ReplaceAllString(cmd, "$1")

	// 3. Convert Makefile-style $(VAR) to shell-style ${VAR}.
	cmd = parenVarRe.ReplaceAllString(cmd, "${$1}")

	// 4. Normalise bare $VAR to ${VAR}.
	cmd = bareVarRe.ReplaceAllString(cmd, "${$1}")

	// 5. Strip leading Dalec-handled env assignments.
	cmd = stripDalecHandledEnvs(cmd)

	// 6. Protect valid ${...} refs, remove stray braces, restore.
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

	// 7. Collapse whitespace and double slashes.
	cmd = collapseSpacesRe.ReplaceAllString(cmd, " ")
	cmd = doubleSlashRe.ReplaceAllString(cmd, "/")
	return strings.TrimSpace(cmd)
}

// stripDalecHandledEnvs removes leading KEY=VALUE shell env assignments for
// variables that Dalec sets via the build env (GOOS, CGO_ENABLED, etc.).
// Only strips tokens at the very start of the command, before the first
// non-assignment word (e.g. "go", "make").
func stripDalecHandledEnvs(cmd string) string {
	for {
		spaceIdx := strings.IndexByte(cmd, ' ')
		if spaceIdx < 0 {
			break
		}
		token := cmd[:spaceIdx]
		eqIdx := strings.IndexByte(token, '=')
		if eqIdx <= 0 {
			break
		}
		if !dalecHandledEnvs[token[:eqIdx]] {
			break
		}
		cmd = strings.TrimSpace(cmd[spaceIdx:])
	}
	return cmd
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

// injectArtifactBinSuffix rewrites only `-o /go/bin/<name>` occurrences where
// `<name>` matches a known artifact binary path from computeArtifactPaths().
// Intermediate helper binaries (compressors, sub-modules) are never artifact
// paths and are skipped.
func injectArtifactBinSuffix(component *workplan.WorkComponent, text string) string {
	artifactPaths := computeArtifactPaths(component)
	return binOutRe.ReplaceAllStringFunc(text, func(match string) string {
		sub := binOutRe.FindStringSubmatch(match)
		if sub == nil {
			return match
		}
		if _, isArtifact := artifactPaths[sub[1]]; isArtifact {
			return "-o " + sub[1] + "${BIN_SUFFIX}"
		}
		return match
	})
}

// ─── Chunk 2 · SUBMODULE TARGETING ───────────────────────────────────────────

// rewriteSubmoduleBuildCd detects a bare `go build` pipeline step whose output
// binary matches a known go-mod-download source and returns a cd path and the
// original step as separate values. The caller emits them as separate lines
// rather than joining with &&.
// Returns ("", step) when no rewrite is needed.
func rewriteSubmoduleBuildCd(step string, downloads []goModDownloadInfo) (string, string) {
	if strings.HasPrefix(step, "cd ") || !strings.HasPrefix(step, "go build") {
		return "", step
	}
	for _, dl := range downloads {
		if strings.Contains(step, "/go/bin/"+dl.SourceKey) {
			return `"$BUILD_ROOT"/` + dl.SourceKey, step
		}
	}
	return "", step
}

// rewriteGoModPath rewrites a go/pkg/mod/<module>@<version> directory path to
// "$BUILD_ROOT"/<sourceKey> using the detected go-mod-download info.
// Used for binary builds whose WORKDIR points into the module cache.
func rewriteGoModPath(dirPath string, downloads []goModDownloadInfo) string {
	for _, dl := range downloads {
		goModPath := "go/pkg/mod/" + dl.ModulePath + "@" + dl.VersionVar
		if strings.Contains(dirPath, goModPath) {
			return `"$BUILD_ROOT"/` + dl.SourceKey
		}
	}
	return `"$BUILD_ROOT"/` + dirPath
}

// rewriteGoModCdPaths replaces `cd /go/pkg/mod/<module>@<version>` with
// `cd "$BUILD_ROOT"/<sourceKey>`. BUILD_ROOT is set in the preamble to the
// initial working directory (where DALEC extracts sources).
func rewriteGoModCdPaths(step string, downloads []goModDownloadInfo) string {
	for _, dl := range downloads {
		goModPath := "/go/pkg/mod/" + dl.ModulePath + "@" + dl.VersionVar
		step = strings.ReplaceAll(step, goModPath, `"$BUILD_ROOT"/`+dl.SourceKey)
	}
	return step
}

// rewriteRelativeSourceCd rewrites a leading `cd <sourceKey>` to
// `cd "$BUILD_ROOT"/<sourceKey>` when <sourceKey> matches a known
// go-mod-download source. This ensures the path resolves correctly
// even after an earlier absolute cd has changed the working directory.
func rewriteRelativeSourceCd(step string, downloads []goModDownloadInfo) string {
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

// isSubmoduleName returns true if name matches a detected go-mod-download
// sub-module source key. Used to avoid renaming the primary binary when the
// entrypoint comes from a sub-module that is built separately.
func isSubmoduleName(name string, downloads []goModDownloadInfo) bool {
	for _, dl := range downloads {
		if dl.SourceKey == name {
			return true
		}
	}
	return false
}

// ─── Chunk 3 · COMMAND PARSING ───────────────────────────────────────────────

// extractCdDir parses a command of the form "cd X && <rest>" or "cd X\n<rest>".
// Returns (X, rest) when matched, or ("", original line) otherwise.
func extractCdDir(line string) (subdir, stripped string) {
	line = strings.TrimSpace(line)
	if m := cdDirRe.FindStringSubmatch(line); m != nil {
		return m[1], strings.TrimSpace(m[2])
	}
	return "", line
}

// ─── Chunk 4 · STAGE ANALYSIS ────────────────────────────────────────────────

// submoduleStageCopies collects COPY --from instructions from Go builder stages
// (which intermediateStageCopies skips). Returns a map from stage name → cp commands
// to inject after cd-ing into that stage's source directory during the build.
func submoduleStageCopies(stages []contents.Stage) map[string][]string {
	stageRefs := make(map[string]bool)
	for _, s := range stages {
		if s.Name != "" {
			stageRefs[s.Name] = true
		}
	}
	goStageNames := make(map[string]bool)
	for _, s := range stages {
		if s.Name != "" && parser.IsGoImage(s.From) {
			goStageNames[s.Name] = true
		}
	}
	result := make(map[string][]string)
	for _, stage := range stages {
		if !parser.IsGoImage(stage.From) && !goStageNames[stage.From] {
			continue
		}
		if stage.Name == "" {
			continue
		}
		for _, cp := range stage.Copies {
			if cp.From == "" || !stageRefs[cp.From] {
				continue
			}
			srcs := strings.Join(cp.Source, " ")
			result[stage.Name] = append(result[stage.Name],
				fmt.Sprintf("cp %s %s", srcs, cp.Dest))
		}
	}
	return result
}

// intermediateStageCopies extracts COPY --from instructions from non-Go,
// non-final intermediate stages and returns them as cp shell commands keyed
// by the stage's WORKDIR. Only COPY --from references that target another
// parsed stage (i.e. cross-stage copies) are included.
//
// Source path rewriting for Dalec sandbox layout:
//   - Paths under /<baseDir>/... (the builder's cwd) → relative path (strip prefix).
//   - Paths under the repo root but outside baseDir → "$BUILD_ROOT"/repo/...
//   - All other absolute paths → kept as-is.
func intermediateStageCopies(stages []contents.Stage, baseDir string) map[string][]string {
	if len(stages) == 0 {
		return nil
	}

	stageRefs := make(map[string]bool)
	for i, s := range stages {
		if s.Name != "" {
			stageRefs[s.Name] = true
		}
		stageRefs[fmt.Sprintf("%d", i)] = true
	}

	baseDirPrefix := "/" + baseDir + "/"
	repoRoot := baseDir
	if idx := strings.IndexByte(baseDir, '/'); idx > 0 {
		repoRoot = baseDir[:idx]
	}
	repoPrefix := "/" + repoRoot + "/"

	result := make(map[string][]string)

	for i, stage := range stages {
		if parser.IsGoImage(stage.From) {
			continue
		}
		if strings.EqualFold(stage.From, "scratch") {
			continue
		}
		if i == len(stages)-1 {
			continue
		}
		if stage.Workdir == "" {
			continue
		}

		var cpCmds []string
		for _, cp := range stage.Copies {
			if cp.From == "" || !stageRefs[cp.From] {
				continue
			}
			var rewritten []string
			for _, src := range cp.Source {
				switch {
				case strings.HasPrefix(src, baseDirPrefix):
					src = strings.TrimPrefix(src, baseDirPrefix)
				case strings.HasPrefix(src, repoPrefix):
					src = `"$BUILD_ROOT"/` + strings.TrimPrefix(src, "/")
				case strings.HasPrefix(src, "/"+cp.From+"/") || src == "/"+cp.From:
					src = src[1:]
				}
				rewritten = append(rewritten, src)
			}
			srcs := strings.Join(rewritten, " ")
			cpCmds = append(cpCmds, bareVarRe.ReplaceAllString(fmt.Sprintf("cp %s %s", srcs, cp.Dest), "${$1}"))
		}
		if len(cpCmds) > 0 {
			result[stage.Workdir] = append(result[stage.Workdir], cpCmds...)
		}
	}

	return result
}

// stageWorkdirs collects non-standard WORKDIR paths from Dockerfile stages that
// need to be created before pipeline steps run. It deduplicates against dirs
// already `mkdir -p`'d in pipelineSteps and excludes standard build paths.
func stageWorkdirs(stages []contents.Stage, pipelineSteps []string, baseDir string) []string {
	candidates := map[string]bool{}
	for _, stage := range stages {
		wd := strings.TrimSpace(stage.Workdir)
		if wd == "" || !strings.HasPrefix(wd, "/") {
			continue
		}
		if standardWorkdirs[wd] {
			continue
		}
		if strings.HasPrefix(wd, "/go/pkg/mod/") {
			continue
		}
		repoRoot := baseDir
		if idx := strings.IndexByte(baseDir, '/'); idx > 0 {
			repoRoot = baseDir[:idx]
		}
		if wd == "/"+repoRoot || strings.HasPrefix(wd, "/"+repoRoot+"/") {
			continue
		}
		candidates[wd] = true
	}

	if len(candidates) == 0 {
		return nil
	}

	// Exclude dirs already handled via mkdir -p in pipeline steps.
	for _, step := range pipelineSteps {
		step = strings.TrimSpace(step)
		if strings.HasPrefix(step, "mkdir") {
			fields := strings.Fields(step)
			for _, f := range fields {
				if f == "mkdir" || f == "-p" || f == "-m" {
					continue
				}
				if strings.HasPrefix(f, "/") {
					delete(candidates, f)
				} else {
					delete(candidates, "/"+f)
				}
			}
		}
	}

	// Drop any WORKDIR that no pipeline step actually references.
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
