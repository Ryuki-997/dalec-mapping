package transformer

// ═══════════════════════════════════════════════════════════════════════════════
// buildUtils.go — Utility functions used during Dockerfile build-step generation.
//
//   Chunk 1 · STRING REWRITERS
//     cleanBuildCommand()          — inlines ldflags, strips env assignments, cleans whitespace
//     stripDalecHandledEnvs()      — strips GOOS/CGO_ENABLED/etc. env prefixes
//     normalizeBareVars()          — rewrites $VAR → ${VAR}
//     stripGoModDownloadPrefix()   — removes go mod download prefix handled as source
//     injectArtifactBinSuffix()    — appends ${BIN_SUFFIX} to artifact -o paths
//
//   Chunk 2 · SUBMODULE TARGETING
//     rewriteSubmoduleBuildCd()    — returns (cdPath, step) for submodule go builds
//     rewriteGoModPath()           — rewrites go/pkg/mod/… path → "$BUILD_ROOT"/<sourceKey>
//     rewriteGoModCdPaths()        — /go/pkg/mod/… → "$BUILD_ROOT"/<sourceKey> in full commands
//     rewriteRelativeSourceCd()    — bare cd <sourceKey> → cd "$BUILD_ROOT"/<sourceKey>
//     isSubmoduleName()            — checks if name matches a go-mod-download source
//
//   Chunk 3 · COMMAND PARSING
//     binSuffixPreamble()          — shell preamble setting BIN_SUFFIX + BUILD_ROOT
//     extractCdDir()               — splits "cd X && rest" into (X, rest); pipeline steps only
//     extractOutputFlag()          — extracts -o path from a go build command
//     scanVarReferences()          — finds ${VAR} refs in merged command text
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
	"dalec-mapping/infrastructure/parser"
)

// cdDirRe matches `cd <dir> && <rest>` or `cd <dir>\n<rest>` patterns.
var cdDirRe = regexp.MustCompile(`(?s)^cd\s+(\S+)\s*(?:&&|\n)\s*(.+)$`)

// binOutRe matches -o /go/bin/<name> in build commands (no variable refs in the name portion).
var binOutRe = regexp.MustCompile(`-o (/go/bin/[^${}\s]+)`)

// ─── Chunk 1 · STRING REWRITERS ──────────────────────────────────────────────

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

	// 1. Inline ldflags — wrap in double quotes when the placeholder isn't
	//    already inside quotes (e.g. `-ldflags ${LDFLAGS}` vs `-ldflags "${LDFLAGS}"`).
	cleanedLd := strings.Trim(ldflags, `"'`)
	cmd = strings.ReplaceAll(cmd, "${LDFLAGS}", `"`+cleanedLd+`"`)

	// 2. Strip all single quotes from the command.
	//    Dalec build steps run in a controlled sandbox — single quotes from
	//    Makefile LDFLAGS (e.g. -X 'pkg.var=${VERSION}') are unnecessary and
	//    can cause issues when unpaired.
	cmd = strings.ReplaceAll(cmd, "'", "")

	// 3. Strip inner double quotes wrapping $VAR / ${VAR} references.
	//    e.g. "$VERSION" → ${VERSION}, "$CNS_AI_PATH"="$CNS_AI_ID" → ${CNS_AI_PATH}=${CNS_AI_ID}
	innerQuotedVar := regexp.MustCompile(`"(\$\{?\w+\}?)"`)
	cmd = innerQuotedVar.ReplaceAllString(cmd, "$1")

	// 3. Normalise bare $VAR to ${VAR} (skip ${ which is already braced).
	cmd = bareVarRe.ReplaceAllString(cmd, "${$1}")

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

// dalecHandledEnvs lists env vars that Dalec sets natively and must be stripped
// from parsed build commands.
var dalecHandledEnvs = map[string]bool{
	"CGO_ENABLED": true, "GOOS": true, "GOARCH": true,
	"GOARM": true, "GOARM64": true, "OS": true, "ARCH": true,
	"GO111MODULE": true,
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

// bareVarRe matches bare $VAR references with uppercase names (dalec args),
// skipping lowercase shell variables like $f in for-loops.
var bareVarRe = regexp.MustCompile(`\$([A-Z_][A-Z0-9_]*)`)

// normalizeBareVars rewrites bare $VAR references to ${VAR} for consistency.
// Only targets uppercase variable names (dalec args), leaving shell variables
// like $f untouched.
func normalizeBareVars(s string) string {
	return bareVarRe.ReplaceAllString(s, "${$1}")
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
// This is semantically correct regardless of build order: intermediate helper
// binaries (compressors, sub-modules) are never artifact paths and are skipped.
func injectArtifactBinSuffix(text string) string {
	artifactPaths := computeArtifactPaths()
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
	if strings.HasPrefix(step, "cd ") {
		return "", step
	}
	if !strings.HasPrefix(step, "go build") {
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
	// No match — return with $BUILD_ROOT prefix stripped of go/pkg/mod.
	return `"$BUILD_ROOT"/` + dirPath
}

// rewriteGoModCdPaths replaces `cd /go/pkg/mod/<module>@<version>` with
// `cd "$BUILD_ROOT"/<sourceKey>`. BUILD_ROOT is set in the preamble to the
// initial working directory (where DALEC extracts sources).
func rewriteGoModCdPaths(step string, downloads []goModDownloadInfo) string {
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

// isSubmoduleName returns true if name matches a detected go-mod-download sub-module
// source key. Used to avoid renaming the primary binary when the entrypoint comes
// from a sub-module that is built separately.
func isSubmoduleName(name string, downloads []goModDownloadInfo) bool {
	for _, dl := range downloads {
		if dl.SourceKey == name {
			return true
		}
	}
	return false
}

// ─── Chunk 3 · COMMAND PARSING ───────────────────────────────────────────────

// binSuffixPreamble returns the shell preamble that sets BIN_SUFFIX.
func binSuffixPreamble() string {
	return `BUILD_ROOT="$PWD"
BIN_SUFFIX=""
OS="linux"
if [ "${GOOS}" = "windows" ]; then
  BIN_SUFFIX=".exe"
  OS="windows"
fi`
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

// extractOutputFlag extracts the path passed to -o in a go build command.
func extractOutputFlag(cmd string) string {
	re := regexp.MustCompile(`\s-o\s+(\S+)`)
	if m := re.FindStringSubmatch(cmd); m != nil {
		return m[1]
	}
	return ""
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

// ─── Chunk 4 · STAGE ANALYSIS ────────────────────────────────────────────────

// standardWorkdirs lists working directories that are always available in the
// build sandbox and never need an explicit mkdir.
var standardWorkdirs = map[string]bool{
	"/":       true,
	"/go":     true,
	"/go/src": true,
	"/go/bin": true,
}

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
	// Build a set of stage names whose base image is a Go SDK image.
	// Stages that reference these aliases (FROM go) are also Go builder stages.
	goStageNames := make(map[string]bool)
	for _, s := range stages {
		if s.Name != "" && parser.IsGoImage(s.From) {
			goStageNames[s.Name] = true
		}
	}
	result := make(map[string][]string)
	for _, stage := range stages {
		if !parser.IsGoImage(stage.From) && !goStageNames[stage.From] {
			continue // only process Go builder stages (the ones intermediateStageCopies skips)
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
//     e.g. /repo/cni/foo → foo   (since cwd = "$BUILD_ROOT"/repo/cni)
//   - Paths under the repo root but outside baseDir → "$BUILD_ROOT"/repo/...
//   - All other absolute paths → kept as-is.
func intermediateStageCopies(stages []contents.Stage, baseDir string) map[string][]string {
	if len(stages) == 0 {
		return nil
	}

	// Build set of all stage names/indices so we can identify cross-stage refs.
	stageRefs := make(map[string]bool)
	for i, s := range stages {
		if s.Name != "" {
			stageRefs[s.Name] = true
		}
		stageRefs[fmt.Sprintf("%d", i)] = true
	}

	// baseDirPrefix: absolute path of the builder's working directory inside the
	// Dockerfile stage filesystem.  Sources under this prefix are addressable by
	// relative path because the build script has already `cd`'d there.
	baseDirPrefix := "/" + baseDir + "/"

	// repoPrefix: top-level repo directory inside the Dockerfile stage filesystem.
	// Sources under this prefix (but outside baseDirPrefix) need "$BUILD_ROOT"/...
	// so the shell can resolve them regardless of the current working directory.
	repoRoot := baseDir
	if idx := strings.IndexByte(baseDir, '/'); idx > 0 {
		repoRoot = baseDir[:idx]
	}
	repoPrefix := "/" + repoRoot + "/"

	result := make(map[string][]string)

	for i, stage := range stages {
		// Skip Go builder stages — their COPY outputs are the main binaries.
		if parser.IsGoImage(stage.From) {
			continue
		}
		// Skip scratch stages (final images).
		if strings.EqualFold(stage.From, "scratch") {
			continue
		}
		// Skip the very last stage.
		if i == len(stages)-1 {
			continue
		}
		// Need a WORKDIR to key on.
		if stage.Workdir == "" {
			continue
		}

		var cpCmds []string
		for _, cp := range stage.Copies {
			if cp.From == "" || !stageRefs[cp.From] {
				continue
			}
			// Rewrite source paths from the Docker stage filesystem to
			// the Dalec sandbox layout.
			var rewritten []string
			for _, src := range cp.Source {
				switch {
				case strings.HasPrefix(src, baseDirPrefix):
					// Already in cwd — use relative path.
					src = strings.TrimPrefix(src, baseDirPrefix)
				case strings.HasPrefix(src, repoPrefix):
					// Elsewhere in the repo — anchor with $BUILD_ROOT.
					src = `"$BUILD_ROOT"/` + strings.TrimPrefix(src, "/")
				case strings.HasPrefix(src, "/"+cp.From+"/") || src == "/"+cp.From:
					// Path lives in the source stage's own workdir (e.g. /azure-ipam/*.conflist
					// from a stage named azure-ipam). Strip the leading "/" to make it
					// relative to the Dalec source root.
					src = src[1:]
				}
				rewritten = append(rewritten, src)
			}
			srcs := strings.Join(rewritten, " ")
			cpCmds = append(cpCmds, normalizeBareVars(fmt.Sprintf("cp %s %s", srcs, cp.Dest)))
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
		// The entire repo source tree is already present in the mounted source —
		// no mkdir needed for the repo root or any subdirectory within it.
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
			// Extract paths from "mkdir -p /path1 /path2 ..." or "mkdir -p rel ..."
			fields := strings.Fields(step)
			for _, f := range fields {
				if f == "mkdir" || f == "-p" || f == "-m" {
					continue
				}
				if strings.HasPrefix(f, "/") {
					delete(candidates, f)
				} else {
					// Pipeline steps may use relative paths; match against absolute candidates.
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
