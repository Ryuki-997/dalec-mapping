package transformer

// ═══════════════════════════════════════════════════════════════════════════════
// extractBuild.go — Generates the `build:` section (env + steps) of a Dalec spec.
//
//   Chunk 1 · ORCHESTRATION          extractBuildSection()
//     Assembles env + steps into the build map, then scans for ${VAR} refs
//     so the caller can promote them to top-level args.
//     Calls → buildEnv(), buildSteps(), scanVarReferences()
//
//   Chunk 2 · ENVIRONMENT            buildEnv()
//     Static Go env vars (GOPROXY, CGO_ENABLED, etc.).
//
//   Chunk 3 · COMMAND ASSEMBLY       buildSteps(), fallbackBuildStep(), resolveFallbackBinaryName(),
//                                     parseBuildLines(), emitPipelineSteps(), filterAlreadyCopied(),
//                                     emitDeferredBuilds()
//     Merges per-binary build commands and pipeline steps into one shell script.
//     Order: preamble → cd baseDir → normal binaries → pipeline steps → deferred sub-modules.
//     buildSteps() orchestrates; parseBuildLines() classifies each raw command;
//     emitPipelineSteps() processes intermediate/wrapper stages with copy injection;
//     emitDeferredBuilds() appends submodule builds after pipeline steps.
//
//   Chunk 4 · PER-BINARY PROCESSING  rawBuildCommands()
//     Cleans each binary's fields and returns one command string per binary.
//     Calls → cleanBuildCommand(), entrypointBinaryName(), isSubmoduleName()
//
//   Utility functions live in buildUtils.go.
// ═══════════════════════════════════════════════════════════════════════════════

import (
	"dalec-mapping/pipeline"
	"fmt"
	"strings"
)

// ─── Chunk 1 · ORCHESTRATION ─────────────────────────────────────────────────

// extractBuildSection assembles the top-level `build:` map for a Dalec spec.
// Returns the build map and the set of ${VAR} names referenced inside it,
// so the caller can forward them as top-level args.
func extractBuildSection(goModDownloads []goModDownloadInfo) (map[string]interface{}, map[string]bool) {
	build := make(map[string]interface{})

	env := buildEnv()
	build["env"] = env

	steps, scanText := buildSteps(goModDownloads)
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
		if _, exists := pipeline.Current.Makefile.Variables[varName]; exists {
			env[varName] = fmt.Sprintf("${%s}", varName)
		}
	}

	return build, referencedVars
}

// ─── Chunk 2 · ENVIRONMENT ───────────────────────────────────────────────────

// buildEnv constructs the env map for the build section.
// Standard Go build vars are always included.
func buildEnv() map[string]interface{} {
	return map[string]interface{}{
		"GOPROXY":      "${GOPROXY}",
		"GOEXPERIMENT": "systemcrypto",
		"CGO_ENABLED":  "1", // required by GOEXPERIMENT=systemcrypto (FIPS)
		"VERSION":      "${VERSION}",
		"GOOS":         "${TARGETOS}",
		"GOARCH":       "${TARGETARCH}",
	}
}

// ─── Chunk 3 · COMMAND ASSEMBLY ──────────────────────────────────────────────

// buildLine holds a single build command with its resolved cd path.
type buildLine struct {
	cdPath      string // full cd path (baseDir or baseDir/subdir)
	command     string // the go build command (without cd prefix)
	isSubmodule bool   // true for go-mod-download sub-module builds (deferred after pipeline)
}

// buildSteps converts extracted binaries into a single Dalec `steps` entry.
// All binaries are merged into one command block so BIN_SUFFIX is set once.
// Also returns the combined command text for var scanning.
// The first step after the preamble is always `cd <baseDir>` where baseDir is
// repo (or repo/componentPath when a component is set).
func buildSteps(goModDownloads []goModDownloadInfo) ([]map[string]interface{}, string) {
	repoInfo := pipeline.Current.RepoInfo

	baseDir := repoInfo.Repo
	if repoInfo.ComponentPath != "" {
		baseDir = repoInfo.Repo + "/" + repoInfo.ComponentPath
	}

	rawCmds := rawBuildCommands(goModDownloads)

	// When ComponentPath is set, go.mod is at the component root — no extra subdir needed
	// because baseDir already includes it.
	goModSubdir := ""
	if repoInfo.ComponentPath == "" {
		goModSubdir = resolveGoModSubpath()
	}

	if len(rawCmds) == 0 {
		cdTarget := baseDir
		if goModSubdir != "" {
			cdTarget = baseDir + "/" + goModSubdir
		}
		// Use Makefile go build target when available (e.g. "./cmd/client") instead
		// of "." which fails when the go.mod directory has no Go files at root.
		buildTarget := "."
		if len(pipeline.Current.Makefile.GoBuildTargets) > 0 {
			buildTarget = pipeline.Current.Makefile.GoBuildTargets[0]
		}
		return fallbackBuildStep(cdTarget, buildTarget)
	}

	allBuildLines := parseBuildLines(rawCmds, baseDir, goModSubdir, goModDownloads)

	if len(allBuildLines) == 0 {
		return fallbackBuildStep(baseDir, ".")
	}

	// Split into normal binaries and deferred sub-module builds.
	// Sub-module builds (e.g. dropgz) depend on pipeline steps that prepare
	// embedded content (/payload), so they must run AFTER pipeline steps.
	var normalLines, deferredLines []buildLine
	for _, bl := range allBuildLines {
		if bl.isSubmodule {
			deferredLines = append(deferredLines, bl)
		} else {
			normalLines = append(normalLines, bl)
		}
	}

	parts := []string{binSuffixPreamble(), "cd " + baseDir}

	// Emit normal binary builds. Only emit cd when the path changes from baseDir.
	lastCd := baseDir
	for _, bl := range normalLines {
		if bl.cdPath != lastCd {
			parts = append(parts, "cd "+bl.cdPath)
			lastCd = bl.cdPath
		}
		parts = append(parts, bl.command)
	}

	submodCopies := submoduleStageCopies(pipeline.Current.Dockerfile.Stages)
	parts = emitPipelineSteps(parts, baseDir, goModDownloads, submodCopies)
	parts = emitDeferredBuilds(parts, deferredLines, submodCopies)

	// Final pass: inject ${BIN_SUFFIX} on only the LAST `-o /go/bin/<name>`
	// across the entire assembled command block.
	stepCmd := strings.Join(parts, "\n")
	stepCmd = strings.ReplaceAll(stepCmd, "'", "")
	stepCmd = injectArtifactBinSuffix(stepCmd)
	return []map[string]interface{}{{"command": stepCmd}}, stepCmd
}

// fallbackBuildStep builds a synthetic go-build command when no parsed binaries exist.
func fallbackBuildStep(cdTarget, buildTarget string) ([]map[string]interface{}, string) {
	binaryName := resolveFallbackBinaryName()
	fallback := fmt.Sprintf("%s\ncd %s\ngo build -o /go/bin/%s${BIN_SUFFIX} %s", binSuffixPreamble(), cdTarget, binaryName, buildTarget)
	return []map[string]interface{}{{"command": fallback}}, fallback
}

// resolveFallbackBinaryName returns the binary name for a synthetic fallback build.
// Uses the first parsed binary name when available, otherwise the repo name.
func resolveFallbackBinaryName() string {
	repoInfo := pipeline.Current.RepoInfo
	binaryName := repoInfo.Repo
	if pipeline.Current.Spec != nil && len(pipeline.Current.Spec.Binaries) > 0 && pipeline.Current.Spec.Binaries[0].Name != "" {
		binaryName = pipeline.Current.Spec.Binaries[0].Name
	}
	return binaryName
}

// parseBuildLines converts raw command strings into structured build lines.
// Each raw command is classified as a normal binary build or a deferred
// submodule build, with its cd path resolved.
func parseBuildLines(rawCmds []string, baseDir, goModSubdir string, goModDownloads []goModDownloadInfo) []buildLine {
	var lines []buildLine
	for _, raw := range rawCmds {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}

		// Check if the binary was built from a go/pkg/mod/ WORKDIR (submodule).
		// The parser encodes this as a leading `cd /go/pkg/mod/... &&` prefix.
		if strings.HasPrefix(raw, "cd /go/pkg/mod/") {
			subdir, rest := extractCdDir(raw)
			subdir = strings.TrimPrefix(subdir, "/")
			rewrittenPath := rewriteGoModPath(subdir, goModDownloads)
			lines = append(lines, buildLine{cdPath: rewrittenPath, command: rest, isSubmodule: true})
			continue
		}

		// Strip any leading cd prefix from the command.
		subdir, cmd := extractCdDir(raw)
		if subdir != "" {
			subdir = strings.TrimPrefix(subdir, "/")
		} else {
			cmd = raw
		}

		cdPath := baseDir
		if subdir != "" && subdir != baseDir {
			// Raw command had an explicit inner cd to a non-baseDir path.
			cmd = "cd " + subdir + "\n" + cmd + "\ncd .."
		} else if subdir == "" && goModSubdir != "" && strings.HasSuffix(cmd, " .") {
			// Build command targets "." — Go files live in <goModSubdir>, not the repo root.
			cmd = "cd " + goModSubdir + "\n" + cmd + "\ncd .."
		}
		lines = append(lines, buildLine{cdPath: cdPath, command: cmd})
	}
	return lines
}

// emitPipelineSteps appends processed pipeline steps (from intermediate and
// wrapper Dockerfile stages) to the command parts. Handles directory navigation,
// copy injection, env stripping, and go-mod path rewriting.
func emitPipelineSteps(parts []string, baseDir string, goModDownloads []goModDownloadInfo, submodCopies map[string][]string) []string {
	if pipeline.Current.Spec == nil || len(pipeline.Current.Spec.PipelineSteps) == 0 {
		return parts
	}

	if mkdirs := stageWorkdirs(pipeline.Current.Dockerfile.Stages, pipeline.Current.Spec.PipelineSteps, baseDir); len(mkdirs) > 0 {
		parts = append(parts, "mkdir -p "+strings.Join(mkdirs, " "))
	}

	workdirCopies := intermediateStageCopies(pipeline.Current.Dockerfile.Stages, baseDir)
	workdirCopies = filterAlreadyCopied(workdirCopies, pipeline.Current.Spec.PipelineSteps)

	for _, step := range pipeline.Current.Spec.PipelineSteps {
		step = strings.TrimSpace(step)
		if step == "" {
			continue
		}
		if goModDownloadRe.MatchString(step) {
			continue
		}
		step = stripDalecHandledEnvs(step)
		step = normalizeBareVars(step)
		step = rewriteGoModCdPaths(step, goModDownloads)
		step = rewriteRelativeSourceCd(step, goModDownloads)
		submodCdPrefix, step := rewriteSubmoduleBuildCd(step, goModDownloads)

		cDir, restStep := extractCdDir(step)
		// Prefer the submodule cd prefix when present (bare go build that was
		// rewritten). Otherwise use the cd extracted from the step itself.
		if submodCdPrefix != "" {
			cDir = submodCdPrefix
			restStep = step
		}

		if cDir != "" && restStep != "" {
			if cpCmds, ok := workdirCopies[cDir]; ok {
				parts = append(parts, cpCmds...)
				delete(workdirCopies, cDir)
			}
			parts = append(parts, "cd "+cDir)
			bare := strings.TrimPrefix(cDir, `"$BUILD_ROOT"/`)
			if cpCmds, ok := submodCopies[bare]; ok {
				parts = append(parts, cpCmds...)
			}
			parts = append(parts, restStep)
		} else {
			if strings.HasPrefix(step, "cd ") {
				dirToken := strings.Fields(strings.TrimPrefix(step, "cd "))[0]
				if cpCmds, ok := workdirCopies[dirToken]; ok {
					parts = append(parts, cpCmds...)
					delete(workdirCopies, dirToken)
				}
			}
			parts = append(parts, step)
		}
	}
	return parts
}

// filterAlreadyCopied removes workdir copy entries whose destination is already
// handled by an explicit `cp` in the pipeline steps.
func filterAlreadyCopied(workdirCopies map[string][]string, pipelineSteps []string) map[string][]string {
	for dir := range workdirCopies {
		for _, raw := range pipelineSteps {
			raw = strings.TrimSpace(raw)
			if !strings.HasPrefix(raw, "cp ") {
				continue
			}
			fields := strings.Fields(raw)
			if len(fields) < 3 {
				continue
			}
			dest := fields[len(fields)-1]
			if strings.HasPrefix(dest, dir+"/") || dest == dir {
				delete(workdirCopies, dir)
				break
			}
		}
	}
	return workdirCopies
}

// emitDeferredBuilds appends deferred submodule builds after pipeline steps.
// Injects cross-stage copies (e.g. COPY --from=compressor) before each build
// so embedded content is in place.
func emitDeferredBuilds(parts []string, deferredLines []buildLine, submodCopies map[string][]string) []string {
	for _, bl := range deferredLines {
		parts = append(parts, "cd "+bl.cdPath)
		bare := strings.TrimPrefix(bl.cdPath, `"$BUILD_ROOT"/`)
		if cpCmds, ok := submodCopies[bare]; ok {
			parts = append(parts, cpCmds...)
		}
		parts = append(parts, bl.command)
	}
	return parts
}

// ─── Chunk 4 · PER-BINARY PROCESSING ─────────────────────────────────────────

// rawBuildCommands cleans each binary's fields and returns one command string per binary.
// Output is always /go/bin/<canonicalName>${BIN_SUFFIX} — the canonical name is derived
// from the primary linux entrypoint when it differs from the parsed binary name (e.g.
// "dropgz" when binaries[0].Name is "azure-ipam"). BIN_SUFFIX is injected so the same
// step works for both Linux (BIN_SUFFIX="") and windowscross (BIN_SUFFIX=".exe").
func rawBuildCommands(goModDownloads []goModDownloadInfo) []string {
	if pipeline.Current.Spec == nil {
		return nil
	}

	epBase := entrypointBinaryName(pipeline.Current.Spec)

	var cmds []string

	for i := range pipeline.Current.Spec.Binaries {
		aux := &pipeline.Current.Spec.Binaries[i]
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

		if cmd != "" && epBase != "" && epBase != aux.Name && len(pipeline.Current.Spec.Binaries) == 1 && !isSubmoduleName(epBase, goModDownloads) {
			cmd = strings.ReplaceAll(cmd,
				"/go/bin/"+aux.Name+"${BIN_SUFFIX}",
				"/go/bin/"+epBase+"${BIN_SUFFIX}",
			)
			cmd = strings.ReplaceAll(cmd,
				"/go/bin/"+aux.Name,
				"/go/bin/"+epBase,
			)
		}

		if cmd != "" {
			cmds = append(cmds, cmd)
		}
	}
	return cmds
}
