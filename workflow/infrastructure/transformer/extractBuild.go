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
//     Calls → cleanBuildCommand(), canonicalBase(), isSubmoduleName()
//
//   Utility functions live in buildUtils.go.
// ═══════════════════════════════════════════════════════════════════════════════

import (
	"fmt"
	"strings"

	"dalec-mapping/workflow/infrastructure/ado"

	domainRepo "dalec-mapping/domain/repository"
	"dalec-mapping/domain/workplan"
)

// ─── Chunk 1 · ORCHESTRATION ─────────────────────────────────────────────────

// extractBuildSection assembles the top-level `build:` map for a Dalec spec.
// Returns the build map and the set of ${VAR} names referenced inside it,
// so the caller can forward them as top-level args.
func extractBuildSection(component *workplan.WorkComponent, goModDownloads []goModDownloadInfo) (map[string]interface{}, map[string]bool) {
	build := make(map[string]interface{})

	env := buildEnv(component)
	build["env"] = env

	steps, scanText := buildSteps(component, goModDownloads)
	build["steps"] = steps

	referencedVars := make(map[string]bool)
	for _, m := range varRefRe.FindAllStringSubmatch(scanText, -1) {
		referencedVars[m[1]] = true
	}

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
		if _, exists := component.BuildFiles.Makefile.Variables[varName]; exists {
			env[varName] = fmt.Sprintf("${%s}", varName)
		}
	}

	return build, referencedVars
}

// ─── Chunk 2 · ENVIRONMENT ───────────────────────────────────────────────────

// buildEnv constructs the env map for the build section.
// Standard Go build vars are always included. ADO repos additionally
// get GONOSUMCHECK and GONOSUMDB to bypass the checksum database for
// private modules.
func buildEnv(component *workplan.WorkComponent) map[string]interface{} {
	env := map[string]interface{}{
		"GOPROXY":      "${GOPROXY}",
		"GOEXPERIMENT": "systemcrypto",
		"CGO_ENABLED":  "1", // required by GOEXPERIMENT=systemcrypto (FIPS)
		"VERSION":      "${VERSION}",
		"GOOS":         "${TARGETOS}",
		"GOARCH":       "${TARGETARCH}",
	}

	repoInfo := component.BuildFiles.RepoInfo
	if ado.IsADORepo(repoInfo.GitURL) && repoInfo.Generator == domainRepo.GoModGenerator {
		domain := extractADODomain(repoInfo.GitURL)
		env["GONOSUMCHECK"] = domain + "/*"
		env["GONOSUMDB"] = domain + "/*"
	}

	return env
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
func buildSteps(component *workplan.WorkComponent, goModDownloads []goModDownloadInfo) ([]map[string]interface{}, string) {
	repoInfo := component.BuildFiles.RepoInfo

	baseDir := repoInfo.Repo
	if repoInfo.ComponentPath != "" {
		baseDir = repoInfo.Repo + "/" + repoInfo.ComponentPath
	}

	rawCmds := rawBuildCommands(component, goModDownloads)

	if len(rawCmds) == 0 {
		// Use Makefile go build target when available (e.g. "./cmd/client") instead
		// of "." which fails when the go.mod directory has no Go files at root.
		buildTarget := "."
		if len(component.BuildFiles.Makefile.GoBuildTargets) > 0 {
			buildTarget = component.BuildFiles.Makefile.GoBuildTargets[0]
		}
		return fallbackBuildStep(component, baseDir, buildTarget)
	}

	allBuildLines := parseBuildLines(rawCmds, baseDir, goModDownloads)

	if len(allBuildLines) == 0 {
		return fallbackBuildStep(component, baseDir, ".")
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

	parts := []string{binSuffixPreamble, "cd " + baseDir}

	// Emit normal binary builds. Only emit cd when the path changes from baseDir.
	lastCd := baseDir
	for _, bl := range normalLines {
		if bl.cdPath != lastCd {
			parts = append(parts, "cd "+bl.cdPath)
			lastCd = bl.cdPath
		}
		parts = append(parts, bl.command)
	}

	submodCopies := submoduleStageCopies(component.BuildFiles.Dockerfile.Stages)
	parts = emitPipelineSteps(component, parts, baseDir, goModDownloads, submodCopies)
	parts = emitDeferredBuilds(parts, deferredLines, submodCopies)

	// Final pass: inject ${BIN_SUFFIX} on only the LAST `-o /go/bin/<name>`
	// across the entire assembled command block.
	stepCmd := strings.Join(parts, "\n")
	stepCmd = strings.ReplaceAll(stepCmd, "'", "")
	stepCmd = injectArtifactBinSuffix(component, stepCmd)
	return []map[string]interface{}{{"command": stepCmd}}, stepCmd
}

// fallbackBuildStep builds a synthetic go-build command when no parsed binaries exist.
func fallbackBuildStep(component *workplan.WorkComponent, cdTarget, buildTarget string) ([]map[string]interface{}, string) {
	binaryName := resolveFallbackBinaryName(component)
	fallback := fmt.Sprintf("%s\ncd %s\ngo build -o /go/bin/%s${BIN_SUFFIX} %s", binSuffixPreamble, cdTarget, binaryName, buildTarget)
	return []map[string]interface{}{{"command": fallback}}, fallback
}

// resolveFallbackBinaryName returns the binary name for a synthetic fallback build.
// Uses the first parsed binary name when available, then Makefile binaries,
// then the component name (if set), otherwise the repo name.
func resolveFallbackBinaryName(component *workplan.WorkComponent) string {
	binaryName := component.BuildFiles.RepoInfo.Repo
	if component.Name != "" {
		binaryName = component.Name
	}
	if len(component.BuildFiles.Spec.Binaries) > 0 && component.BuildFiles.Spec.Binaries[0].Name != "" {
		binaryName = component.BuildFiles.Spec.Binaries[0].Name
	} else if len(component.BuildFiles.Makefile.GoBuildCommands) > 0 && component.BuildFiles.Makefile.GoBuildCommands[0].Name != "" {
		binaryName = component.BuildFiles.Makefile.GoBuildCommands[0].Name
	}
	return binaryName
}

// makefileBuildCommands returns cleaned go build commands extracted from the Makefile.
// Called when no Dockerfile Spec is available (Makefile-only projects).
func makefileBuildCommands(component *workplan.WorkComponent) []string {
	makefileBinaries := component.BuildFiles.Makefile.GoBuildCommands
	if len(makefileBinaries) == 0 {
		return nil
	}

	var cmds []string
	for _, binary := range makefileBinaries {
		if binary.Name == "" {
			continue
		}

		var cmd string
		if binary.BuildCommand != "" {
			cmd = binary.BuildCommand
		} else if binary.LdFlags != "" {
			cmd = fmt.Sprintf("go build -ldflags \"%s\" -o %s", binary.LdFlags, binary.OutputPath)
		} else {
			cmd = fmt.Sprintf("go build -o %s", binary.OutputPath)
		}

		if cmd != "" {
			cmds = append(cmds, cmd)
		}
	}
	return cmds
}

// parseBuildLines converts raw command strings into structured build lines.
// Each raw command is classified as a normal binary build or a deferred
// submodule build, with its cd path resolved.
func parseBuildLines(rawCmds []string, baseDir string, goModDownloads []goModDownloadInfo) []buildLine {
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
			cmd = "cd " + subdir + "\n" + cmd + "\ncd .."
		}
		lines = append(lines, buildLine{cdPath: cdPath, command: cmd})
	}
	return lines
}

// emitPipelineSteps appends processed pipeline steps (from intermediate and
// wrapper Dockerfile stages) to the command parts. Handles directory navigation,
// copy injection, env stripping, and go-mod path rewriting.
func emitPipelineSteps(component *workplan.WorkComponent, parts []string, baseDir string, goModDownloads []goModDownloadInfo, submodCopies map[string][]string) []string {
	if len(component.BuildFiles.Spec.PipelineSteps) == 0 {
		return parts
	}

	if mkdirs := stageWorkdirs(component.BuildFiles.Dockerfile.Stages, component.BuildFiles.Spec.PipelineSteps, baseDir); len(mkdirs) > 0 {
		parts = append(parts, "mkdir -p "+strings.Join(mkdirs, " "))
	}

	workdirCopies := intermediateStageCopies(component.BuildFiles.Dockerfile.Stages, baseDir)
	workdirCopies = filterAlreadyCopied(workdirCopies, component.BuildFiles.Spec.PipelineSteps)

	for _, step := range component.BuildFiles.Spec.PipelineSteps {
		step = strings.TrimSpace(step)
		if step == "" {
			continue
		}
		if goModDownloadRe.MatchString(step) {
			continue
		}
		step = stripDalecHandledEnvs(step)
		step = parenVarRe.ReplaceAllString(step, "${$1}")
		step = bareVarRe.ReplaceAllString(step, "${$1}")
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
func rawBuildCommands(component *workplan.WorkComponent, goModDownloads []goModDownloadInfo) []string {
	if len(component.BuildFiles.Spec.Binaries) == 0 {
		return makefileBuildCommands(component)
	}

	epBase := canonicalBase(component.BuildFiles.Spec.Symlink)

	var cmds []string

	for i := range component.BuildFiles.Spec.Binaries {
		aux := &component.BuildFiles.Spec.Binaries[i]
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

		if cmd != "" && epBase != "" && epBase != aux.Name && len(component.BuildFiles.Spec.Binaries) == 1 && !isSubmoduleName(epBase, goModDownloads) {
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
