package parser

import (
	"dalec-mapping/domain/contents"
	"fmt"
	"log"
	"os"
	"path"
	"regexp"
	"strings"

	"github.com/moby/buildkit/frontend/dockerfile/parser"
)

/*
How Buildkit Parser Works:
==========================

Instead of manually parsing Dockerfile syntax, we use buildkit's parser which:
1. Handles all Dockerfile syntax rules (backslashes, quotes, JSON arrays, etc.)
2. Returns an AST (Abstract Syntax Tree)
3. We just walk the tree and extract structured data

The AST has this structure:
- result.AST.Children = array of instruction nodes (FROM, RUN, COPY, etc.)
- Each node has:
  * node.Value = instruction name (e.g., "FROM", "RUN")
  * node.Next = linked list of arguments
  * node.Flags = flags like --platform=, --from=
  * node.Attributes = metadata like whether it's JSON format

Example:
  Dockerfile: FROM --platform=linux/amd64 golang:1.21 AS builder
  Buildkit gives us:
    node.Value = "FROM"
    node.Flags = ["--platform=linux/amd64"]
    node.Next.Value = "golang:1.21"
    node.Next.Next.Value = "AS"
    node.Next.Next.Next.Value = "builder"
*/

// ParseDockerfile uses buildkit parser to parse a Dockerfile
// The buildkit parser handles all the complex parsing for us
func ParseDockerfile(dockerfile []byte, info *contents.DockerfileInfo) (*contents.DockerfileInfo, error) {
	// Create a temporary file to store the Dockerfile content
	tmpFile, err := os.CreateTemp("", "Dockerfile")
	if err != nil {
		return nil, fmt.Errorf("failed to create temporary file: %w", err)
	}
	defer os.Remove(tmpFile.Name())
	defer tmpFile.Close()

	// Write the dockerfile content to the temporary file
	if _, err := tmpFile.Write(dockerfile); err != nil {
		return nil, fmt.Errorf("failed to write to temporary file: %w", err)
	}

	// Reset file pointer to the beginning
	if _, err := tmpFile.Seek(0, 0); err != nil {
		return nil, fmt.Errorf("failed to seek in temporary file: %w", err)
	}

	// Docker buildkit parses the entire Dockerfile and returns an AST
	result, err := parser.Parse(tmpFile)
	if err != nil {
		return nil, fmt.Errorf("failed to parse Dockerfile: %w", err)
	}

	var currentStage *contents.Stage

	// Walk the AST - each child is a Dockerfile instruction
	for _, node := range result.AST.Children {
		instruction := strings.ToUpper(node.Value)

		// Add raw instruction to current stage if it exists
		if currentStage != nil {
			rawInst := contents.RawInstruction{
				Type:  instruction,
				Args:  []string{},
				Flags: make(map[string]string),
			}

			// Collect arguments
			for n := node.Next; n != nil; n = n.Next {
				rawInst.Args = append(rawInst.Args, n.Value)
			}

			// Collect flags
			if node.Flags == nil {
				continue
			}

			for _, flag := range node.Flags {
				if !strings.Contains(flag, "=") {
					continue
				}
				parts := strings.SplitN(flag, "=", 2)
				key := strings.TrimPrefix(parts[0], "--")
				rawInst.Flags[key] = parts[1]
			}

			currentStage.Instructions = append(currentStage.Instructions, rawInst)
		}

		switch instruction {
		case "FROM":
			currentStage = parseFromInstruction(node)
			info.Stages = append(info.Stages, *currentStage)
			// Update pointer to the stage in the slice
			currentStage = &info.Stages[len(info.Stages)-1]

		case "ARG":
			key, value := parseKeyValue(node.Next)
			// Preserve a valued global ARG when a stage re-declares without a default.
			// Docker inherits the global default in this case (ARG FOO inside a stage).
			if value != "" || info.Args[key] == "" {
				info.Args[key] = value
			}
			if currentStage != nil {
				currentStage.Args[key] = value
			}

		case "ENV":
			if currentStage != nil {
				key, value := parseKeyValue(node.Next)
				currentStage.Env[key] = value
			}

		case "WORKDIR":
			if currentStage != nil && node.Next != nil {
				currentStage.Workdir = node.Next.Value
			}

		case "RUN":
			if currentStage != nil {
				// buildkit already parsed the command for us
				cmd := reconstructCommand(node.Next)
				currentStage.Runs = append(currentStage.Runs, cmd)
			}

		case "COPY", "ADD":
			if currentStage != nil {
				copy := parseCopyInstruction(node, instruction)
				currentStage.Copies = append(currentStage.Copies, copy)
			}

		case "ENTRYPOINT":
			if currentStage != nil {
				currentStage.Entrypoint = parseCommandArray(node)
			}

		case "CMD":
			if currentStage != nil {
				currentStage.Cmd = parseCommandArray(node)
			}

		case "EXPOSE":
			if currentStage != nil && node.Next != nil {
				currentStage.Expose = append(currentStage.Expose, node.Next.Value)
			}

		case "LABEL":
			key, value := parseKeyValue(node.Next)
			info.Labels[key] = strings.Trim(value, "\"")
		}
	}

	return info, nil
}

// parseFromInstruction extracts information from a FROM instruction
// Example: FROM --platform=linux/amd64 golang:1.21 AS builder
func parseFromInstruction(node *parser.Node) *contents.Stage {
	stage := &contents.Stage{
		Args:         make(map[string]string),
		Env:          make(map[string]string),
		Copies:       []contents.CopyInstruction{},
		Runs:         []string{},
		Expose:       []string{},
		Instructions: []contents.RawInstruction{},
	}

	// Check for flags (buildkit already parsed them)
	if node.Flags != nil {
		for _, flag := range node.Flags {
			if strings.HasPrefix(flag, "--platform=") {
				stage.Platform = strings.TrimPrefix(flag, "--platform=")
			}
		}
	}

	// Get base image (first argument)
	if node.Next != nil {
		stage.From = node.Next.Value

		// Check for "AS <name>" clause
		n := node.Next.Next
		if n != nil && strings.ToUpper(n.Value) == "AS" && n.Next != nil {
			stage.Name = n.Next.Value
		}
	}

	return stage
}

// parseCopyInstruction extracts COPY/ADD instruction details
// Example: COPY --from=builder /app/bin /usr/local/bin
func parseCopyInstruction(node *parser.Node, instType string) contents.CopyInstruction {
	copy := contents.CopyInstruction{
		Type:   instType,
		Source: []string{},
	}

	// Check for --from flag (buildkit already parsed it)
	if node.Flags != nil {
		for _, flag := range node.Flags {
			if strings.HasPrefix(flag, "--from=") {
				copy.From = strings.TrimPrefix(flag, "--from=")
			}
		}
	}

	// Walk through arguments: all but last are sources, last is dest
	var args []string
	for n := node.Next; n != nil; n = n.Next {
		args = append(args, n.Value)
	}

	if len(args) > 0 {
		copy.Dest = args[len(args)-1]
		copy.Source = args[:len(args)-1]
	}

	return copy
}

// parseCommandArray handles both JSON and shell format commands
// buildkit tells us if it's JSON via node.Attributes["json"]
func parseCommandArray(node *parser.Node) []string {
	// Check if buildkit detected JSON format (e.g., ["cmd", "arg1", "arg2"])
	if node.Attributes != nil && node.Attributes["json"] {
		var result []string
		for n := node.Next; n != nil; n = n.Next {
			result = append(result, n.Value)
		}
		return result
	}

	// Shell format - wrap in shell
	cmd := reconstructCommand(node.Next)
	if cmd != "" {
		return []string{"/bin/sh", "-c", cmd}
	}
	return nil
}

// reconstructCommand joins node values back into a single command string
func reconstructCommand(node *parser.Node) string {
	var parts []string
	for n := node; n != nil; n = n.Next {
		parts = append(parts, n.Value)
	}
	return strings.Join(parts, " ")
}

// parseKeyValue extracts key=value or key value pairs
func parseKeyValue(node *parser.Node) (string, string) {
	if node == nil {
		return "", ""
	}

	fullValue := reconstructCommand(node)

	// Try splitting on =
	if strings.Contains(fullValue, "=") {
		parts := strings.SplitN(fullValue, "=", 2)
		return parts[0], parts[1]
	}

	// Try splitting on space
	parts := strings.SplitN(fullValue, " ", 2)
	if len(parts) == 2 {
		return parts[0], parts[1]
	}

	return fullValue, ""
}

func PrintDockerfileInfo(defaultSpec *contents.DefaultSpec) {
	log.Println("Parsed Dockerfile Stages:")
	log.Println("")

	for _, stage := range defaultSpec.Stages {
		log.Printf("Stage: %s (From: %s)\n", stage.Name, stage.From)
		log.Println("  Env:")
		for k, v := range stage.Env {
			log.Printf("    %s=%s\n", k, v)
		}
		log.Println("  Runs:")
		for _, run := range stage.Runs {
			log.Printf("    %s\n", run)
		}
		log.Println("  Copies:")
		for _, copy := range stage.Copies {
			log.Printf("    From: %s, Source: %v, Dest: %s\n", copy.From, copy.Source, copy.Dest)
		}
		log.Println("")
	}

	for k, v := range defaultSpec.Args {
		log.Printf("Build Arg: %s=%s\n", k, v)
	}
	log.Println("")
}

// ═══════════════════════════════════════════════════════════════════════════════
// DOCKERFILE ANALYSIS — Analyses multi-stage Dockerfiles for patterns
// that map to Dalec spec fields.
//
//   Chunk 1 · GO TOOLCHAIN PIN         DetectGoToolchainPin()
//     Detects FROM stages that reference a Go SDK image and extracts the
//     digest/tag. Dalec does not support pinning the Go toolchain — this is
//     emitted as a spec-level comment for traceability.
//
//   Chunk 2 · INTERMEDIATE RUNTIME     ExtractIntermediateRuntimeDeps()
//     Detects intermediate stages that install packages (tdnf, apt-get, …)
//     and are only consumed via COPY --from. Extracts the package names and
//     returns them as runtime dependency candidates.
//
//   Chunk 3 · FINAL LINUX BASE         DetectFinalLinuxBase()
//     Identifies the final Linux stage's base image reference for use in
//     image.bases[].rootfs.image.ref.
// ═══════════════════════════════════════════════════════════════════════════════

// goImagePatterns matches common Go SDK image references.
var goImagePatterns = []string{
	"mcr.microsoft.com/oss/go/microsoft/golang",
	"golang",
	"docker.io/library/golang",
	"docker.io/golang",
}

// pkgInstallRe matches package manager install commands and captures the
// package list. Supports tdnf, yum, dnf, apt-get, and apk.
var pkgInstallRe = regexp.MustCompile(
	`(?:tdnf|yum|dnf|apt-get|apk)\s+(?:install|add)\s+(?:-\S+\s+)*(.+)`)

// windowsImageIndicators are substrings that identify a Windows base image.
var windowsImageIndicators = []string{
	"nanoserver", "servercore", "windows",
}

// ─── Chunk 1 · GO TOOLCHAIN PIN ─────────────────────────────────────────────

// GoToolchainPin holds information about a pinned Go build image.
type GoToolchainPin struct {
	// StageName is the AS alias of the Go stage (e.g. "go", "builder").
	StageName string
	// ImageRef is the full FROM reference (e.g. "mcr.microsoft.com/oss/go/microsoft/golang@sha256:6a56...").
	ImageRef string
	// Digest is the @sha256:... portion, if present.
	Digest string
	// Tag is the :tag portion, if present (e.g. "1.24-azurelinux3.0").
	Tag string
}

// DetectGoToolchainPin scans Dockerfile stages for Go SDK base images and
// returns pin information. Returns nil if no Go stage is found.
func DetectGoToolchainPin(stages []contents.Stage) *GoToolchainPin {
	for _, stage := range stages {
		ref := stage.From
		if !IsGoImage(ref) {
			continue
		}

		pin := &GoToolchainPin{
			StageName: stage.Name,
			ImageRef:  ref,
		}

		// Extract digest (@sha256:...)
		if idx := strings.Index(ref, "@"); idx >= 0 {
			pin.Digest = ref[idx+1:]
		}

		// Extract tag (:tag) — present with or without digest.
		// E.g. "golang:1.24@sha256:..." has both tag and digest.
		// Strip the digest first to find the tag.
		refNoDigest := ref
		if idx := strings.Index(refNoDigest, "@"); idx >= 0 {
			refNoDigest = refNoDigest[:idx]
		}
		if idx := strings.LastIndex(refNoDigest, ":"); idx >= 0 {
			pin.Tag = refNoDigest[idx+1:]
		}

		return pin
	}
	return nil
}

// IsGoImage checks whether an image reference matches a known Go SDK image.
func IsGoImage(ref string) bool {
	lower := strings.ToLower(ref)
	for _, pattern := range goImagePatterns {
		// Match the base image name before any tag/digest separator.
		base := lower
		if idx := strings.Index(base, "@"); idx >= 0 {
			base = base[:idx]
		}
		if idx := strings.LastIndex(base, ":"); idx >= 0 {
			base = base[:idx]
		}
		if base == pattern {
			return true
		}
	}
	return false
}

// GoVersion extracts the Go version string from the pin.
// For tags like "1.24-azurelinux3.0", returns "1.24".
// For digest-only pins, returns "" (version cannot be determined from digest alone).
func (p *GoToolchainPin) GoVersion() string {
	if p == nil {
		return ""
	}
	if p.Tag != "" {
		// Tag format: "1.24-azurelinux3.0" or "1.24.1" or "1.24"
		// Extract the version prefix before any distro suffix.
		ver := p.Tag
		if idx := strings.Index(ver, "-"); idx >= 0 {
			ver = ver[:idx]
		}
		return ver
	}
	return ""
}

// ─── Chunk 2 · INTERMEDIATE RUNTIME ─────────────────────────────────────────

// IntermediateRuntimeDeps holds packages extracted from an intermediate stage.
type IntermediateRuntimeDeps struct {
	// StageName is the AS alias of the intermediate stage.
	StageName string
	// Packages are the package names installed via the package manager.
	Packages []string
	// SelectiveCopy is true when COPY --from selects specific files/dirs
	// rather than copying the full package tree. Flags the entry for review.
	SelectiveCopy bool
}

// ExtractIntermediateRuntimeDeps analyses Dockerfile stages to find intermediate
// stages that install packages and are consumed only via COPY --from.
// Returns the extracted runtime dependency candidates grouped by stage.
func ExtractIntermediateRuntimeDeps(stages []contents.Stage) []IntermediateRuntimeDeps {
	// Build set of stage names/indices referenced by COPY --from.
	copyFromTargets := make(map[string]bool)
	// Track whether the COPY is selective (specific files) vs whole-tree.
	copyFromSelective := make(map[string]bool)

	for _, stage := range stages {
		for _, cp := range stage.Copies {
			if cp.From == "" {
				continue
			}
			copyFromTargets[cp.From] = true

			// Heuristic: if the source is a specific file path (not / or a top-level dir),
			// consider it selective. Patterns like /usr/sbin/*tables* or /usr/lib are selective.
			for _, src := range cp.Source {
				src = strings.TrimSpace(src)
				if src != "/" && src != "." {
					copyFromSelective[cp.From] = true
				}
			}
		}
	}

	// Find the last stage (final image) — it's never an intermediate.
	if len(stages) == 0 {
		return nil
	}
	finalIdx := len(stages) - 1

	var results []IntermediateRuntimeDeps

	for i, stage := range stages {
		if i == finalIdx {
			continue
		}

		// Check if this stage is referenced by COPY --from.
		stageRef := stage.Name
		if stageRef == "" {
			stageRef = fmt.Sprintf("%d", i)
		}
		if !copyFromTargets[stageRef] {
			continue
		}

		// Skip Go/builder stages (they're build stages, not runtime dep providers).
		if IsGoImage(stage.From) {
			continue
		}

		// Look for package install commands in RUN instructions.
		packages := extractPackagesFromRuns(stage.Runs)
		if len(packages) == 0 {
			continue
		}

		results = append(results, IntermediateRuntimeDeps{
			StageName:     stageRef,
			Packages:      packages,
			SelectiveCopy: copyFromSelective[stageRef],
		})

		log.Printf("📦 Intermediate stage %q installs packages: %v (selective=%v)\n",
			stageRef, packages, copyFromSelective[stageRef])
	}

	return results
}

// extractPackagesFromRuns scans RUN commands for package manager install lines
// and returns the deduplicated list of package names.
func extractPackagesFromRuns(runs []string) []string {
	seen := make(map[string]bool)
	var packages []string

	for _, run := range runs {
		// Normalise: collapse shell continuations, split on && and ;.
		normalized := strings.ReplaceAll(run, "\\\n", " ")
		cmds := splitShellCommands(normalized)

		for _, cmd := range cmds {
			cmd = strings.TrimSpace(cmd)
			m := pkgInstallRe.FindStringSubmatch(cmd)
			if m == nil {
				continue
			}
			// m[1] is the package list portion after flags.
			for _, token := range strings.Fields(m[1]) {
				// Skip flags and shell operators.
				if strings.HasPrefix(token, "-") || token == "&&" || token == "||" || token == ";" {
					continue
				}
				pkg := strings.TrimSpace(token)
				if pkg != "" && !seen[pkg] {
					seen[pkg] = true
					packages = append(packages, pkg)
				}
			}
		}
	}
	return packages
}

// splitShellCommands splits a shell line on && and ; delimiters.
func splitShellCommands(s string) []string {
	// Replace ; with && so we can split on one delimiter.
	s = strings.ReplaceAll(s, ";", "&&")
	parts := strings.Split(s, "&&")
	var result []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}

// ─── Chunk 3 · FINAL LINUX BASE ────────────────────────────────────────────

// DetectFinalLinuxBase identifies the last non-Windows, non-Go, non-intermediate
// stage as the final Linux base and returns its image reference.
// Returns "" if no suitable final Linux stage is found.
func DetectFinalLinuxBase(stages []contents.Stage) string {
	if len(stages) == 0 {
		return ""
	}

	// Build set of stages referenced by other stages — either via COPY --from
	// or via FROM (multi-stage base). These are intermediates, not the final image.
	referencedStages := make(map[string]bool)
	for i, stage := range stages {
		for _, cp := range stage.Copies {
			if cp.From != "" {
				referencedStages[cp.From] = true
			}
		}
		// A FROM that references an earlier stage name/index makes that
		// earlier stage an intermediate too.
		for j := 0; j < i; j++ {
			if stages[j].Name != "" && stages[j].Name == stage.From {
				referencedStages[stages[j].Name] = true
			}
			if stage.From == fmt.Sprintf("%d", j) {
				referencedStages[stage.From] = true
			}
		}
	}

	// Walk stages in reverse to find the last Linux final stage.
	for i := len(stages) - 1; i >= 0; i-- {
		stage := stages[i]

		// Skip intermediate stages referenced by COPY --from or FROM.
		stageRef := stage.Name
		if stageRef == "" {
			stageRef = fmt.Sprintf("%d", i)
		}
		if referencedStages[stageRef] {
			continue
		}

		// Skip if base image references an earlier stage (alias or index).
		if isStageSelfReference(stage.From, stages, i) {
			continue
		}

		// Skip Go toolchain images.
		if IsGoImage(stage.From) {
			continue
		}

		// Skip Windows images.
		if IsWindowsImage(stage.From) {
			continue
		}

		// Skip if platform explicitly targets Windows.
		if strings.Contains(strings.ToLower(stage.Platform), "windows") {
			continue
		}

		// Skip scratch — it's a pseudo-image with no manifest; Dalec
		// can't resolve it and will use its own default base instead.
		if strings.EqualFold(stage.From, "scratch") {
			continue
		}

		return stage.From
	}

	return ""
}

// IsWindowsImage returns true if the image reference contains Windows indicators.
func IsWindowsImage(ref string) bool {
	lower := strings.ToLower(ref)
	for _, ind := range windowsImageIndicators {
		if strings.Contains(lower, ind) {
			return true
		}
	}
	return false
}

// isStageSelfReference returns true if ref matches a stage alias or index
// preceding the current stage (i.e. it's a multi-stage FROM referencing
// an earlier stage, not an external image).
func isStageSelfReference(ref string, stages []contents.Stage, currentIdx int) bool {
	for j := 0; j < currentIdx; j++ {
		if stages[j].Name != "" && stages[j].Name == ref {
			return true
		}
		if ref == fmt.Sprintf("%d", j) {
			return true
		}
	}
	return false
}

// ═══════════════════════════════════════════════════════════════════════════════
// STATIC BUILD VALUE EXTRACTION — Deterministic extraction of build values
// from parsed Dockerfile stages.
//
//   Chunk 5 · MAIN              ExtractStaticBuildValues()
//   Chunk 6 · BINARY EXTRACTION extractGoBinaries(), parseGoBuildCommand(),
//                                cleanStaticBuildCommand()
//   Chunk 7 · PIPELINE STEPS    extractPipelineSteps()
//   Chunk 8 · ENTRYPOINT        resolveEntrypoint(), defaultEntrypoints()
//   Chunk 9 · STAGE HELPERS     findBuilderStage(), findFinalStage(),
//                                findIntermediateStages()
// ═══════════════════════════════════════════════════════════════════════════════

// goBuildRe matches `go build` commands in RUN instructions.
var goBuildRe = regexp.MustCompile(`go\s+build\b`)

// goBuildOutputFlagRe captures the -o <path> argument from a go build command.
var goBuildOutputFlagRe = regexp.MustCompile(`-o\s+(\S+)`)

// goLdflagsRe captures the -ldflags "..." argument from a go build command.
var goLdflagsRe = regexp.MustCompile(`-ldflags\s+["']([^"']+)["']`)

// goLdflagsVarRe captures -ldflags ${VAR} (unquoted variable reference).
var goLdflagsVarRe = regexp.MustCompile(`-ldflags\s+(\$\{?\w+\}?)`)

// lineContinuationRe matches shell line continuations.
var lineContinuationRe = regexp.MustCompile(`\\\s*\n\s*`)

// ─── Chunk 5 · MAIN ─────────────────────────────────────────────────────────

// ExtractStaticBuildValues derives a DockerfileSpec from the global contents.Dockerfile
// set by ParseDockerfile. Stores the result in contents.Spec and returns it.
// Returns nil if no Dockerfile stages are available or no Go builder stage is found.
func ExtractStaticBuildValues() *contents.DockerfileSpec {
	stages := contents.Dockerfile.Stages
	globalArgs := contents.Dockerfile.Args

	if len(stages) == 0 {
		return nil
	}

	builderIdx := findBuilderStage(stages)
	if builderIdx < 0 {
		log.Println("⚠️  Static extractor: no Go builder stage found")
		return nil
	}

	binaries := extractGoBinaries(stages[builderIdx], globalArgs)
	pipelineSteps := extractPipelineSteps(stages, builderIdx)
	entrypoint, symlink := resolveEntrypoint(stages, binaries)

	// Build per-target image config.
	var targets []contents.SpecTarget
	targets = append(targets, contents.SpecTarget{
		OS:         "azlinux3",
		Entrypoint: entrypoint,
		Symlink:    symlink,
	})
	// Windows target: bare binary name (transformer adds /Windows/System32/ prefix).
	winEntry := path.Base(entrypoint)
	if winEntry == "." || winEntry == "/" {
		winEntry = path.Base(symlink)
	}
	targets = append(targets, contents.SpecTarget{
		OS:         "windowscross",
		Entrypoint: winEntry,
	})

	spec := &contents.DockerfileSpec{
		Binaries:      binaries,
		PipelineSteps: pipelineSteps,
		Targets:       targets,
	}

	log.Printf("📊 Static extractor: %d binaries, %d pipeline steps, entrypoint=%s, symlink=%s\n",
		len(binaries), len(pipelineSteps), entrypoint, symlink)

	contents.Spec = spec
	return spec
}

// ─── Chunk 6 · BINARY EXTRACTION ─────────────────────────────────────────────

// extractGoBinaries finds all `go build` commands in the builder stage and
// parses them into Binary structs.
func extractGoBinaries(builder contents.Stage, globalArgs map[string]string) []contents.SpecBinary {
	var binaries []contents.SpecBinary

	// Merge global args with stage args and env for variable substitution.
	vars := make(map[string]string)
	for k, v := range globalArgs {
		vars[k] = v
	}
	for k, v := range builder.Args {
		vars[k] = v
	}
	for k, v := range builder.Env {
		vars[k] = v
	}

	for _, run := range builder.Runs {
		// Normalize line continuations.
		run = lineContinuationRe.ReplaceAllString(run, " ")

		// Split on && and ; to find individual commands.
		cmds := splitShellCommands(run)
		for _, cmd := range cmds {
			cmd = strings.TrimSpace(cmd)
			if !goBuildRe.MatchString(cmd) {
				continue
			}

			bin := parseGoBuildCommand(cmd)
			if bin.Name != "" {
				binaries = append(binaries, bin)
			}
		}
	}

	return binaries
}

// parseGoBuildCommand parses a single `go build ...` command into a SpecBinary.
func parseGoBuildCommand(cmd string) contents.SpecBinary {
	bin := contents.SpecBinary{
		BuildCommand: cleanStaticBuildCommand(cmd),
	}

	// Extract -o <path>
	if m := goBuildOutputFlagRe.FindStringSubmatch(cmd); m != nil {
		outputPath := m[1]
		bin.OutputPath = outputPath
		bin.Name = path.Base(strings.TrimSuffix(outputPath, "${BIN_SUFFIX}"))
	}

	// Extract -ldflags "..."
	if m := goLdflagsRe.FindStringSubmatch(cmd); m != nil {
		bin.LdFlags = m[1]
	} else if m := goLdflagsVarRe.FindStringSubmatch(cmd); m != nil {
		bin.LdFlags = m[1]
	}

	// If no -o flag, try to derive name from the last argument (package path).
	if bin.Name == "" {
		fields := strings.Fields(cmd)
		if len(fields) > 0 {
			lastArg := fields[len(fields)-1]
			// The last argument is typically a package path like ./cmd/client/main.go
			// or ./cmd/foo or just .
			if strings.HasPrefix(lastArg, "./") || strings.HasPrefix(lastArg, "/") {
				base := path.Base(lastArg)
				if base != "." && base != "main.go" {
					bin.Name = strings.TrimSuffix(base, ".go")
				}
			}
		}
	}

	return bin
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

// ─── Chunk 7 · PIPELINE STEPS ────────────────────────────────────────────────

// extractPipelineSteps collects RUN commands from intermediate stages
// (non-builder, non-final) that contain build-related operations.
// Package manager install commands are excluded — those are runtime deps
// handled by the transformer's dependency extraction, not build steps.
func extractPipelineSteps(stages []contents.Stage, builderIdx int) []string {
	intermediateIdxs := findIntermediateStages(stages, builderIdx)
	var steps []string

	for _, idx := range intermediateIdxs {
		stage := stages[idx]
		for _, run := range stage.Runs {
			run = lineContinuationRe.ReplaceAllString(run, " ")
			run = strings.TrimSpace(run)
			if run == "" {
				continue
			}
			// Skip package manager installs — these are runtime dependencies,
			// not build pipeline steps.
			if pkgInstallRe.MatchString(run) {
				continue
			}
			steps = append(steps, run)
		}
	}

	return steps
}

// ─── Chunk 8 · ENTRYPOINT ────────────────────────────────────────────────────

// resolveEntrypoint determines the binary's entrypoint and symlink path from
// the final stage's ENTRYPOINT instruction and COPY destinations.
//
// Convention:
//   - symlink = the real installed binary path (e.g. /usr/bin/<name>) → tested with permissions
//   - entrypoint = where the symlink points (e.g. /usr/local/bin/<name>) → the container entrypoint
func resolveEntrypoint(stages []contents.Stage, binaries []contents.SpecBinary) (entrypoint, symlink string) {
	finalIdx := findFinalStage(stages)
	if finalIdx < 0 {
		return defaultEntrypoints(binaries)
	}

	final := stages[finalIdx]

	// Check ENTRYPOINT instruction.
	if len(final.Entrypoint) > 0 {
		ep := final.Entrypoint[0]
		if strings.HasPrefix(ep, "/") {
			entrypoint = ep
		}
	}

	// Look at COPY --from destinations to find installed binary paths.
	for _, cp := range final.Copies {
		if cp.From == "" {
			continue
		}
		dest := cp.Dest
		// Normalize relative paths from scratch-stage copies (e.g. "dropgz" → "/dropgz").
		if !strings.HasPrefix(dest, "/") {
			dest = "/" + dest
		}
		if strings.HasPrefix(dest, "/usr/bin/") || strings.HasPrefix(dest, "/usr/local/bin/") ||
			strings.HasPrefix(dest, "/usr/sbin/") {
			if entrypoint == "" {
				entrypoint = dest
			} else if dest != entrypoint {
				symlink = dest
			}
		} else if !strings.Contains(dest[1:], "/") {
			// Root-level binary (e.g. /dropgz from a scratch stage). Use directly as entrypoint.
			if entrypoint == "" {
				entrypoint = dest
			}
		}
	}

	// Normalize: entrypoint should be /usr/local/bin/<name>, symlink should be /usr/bin/<name>.
	if entrypoint != "" && symlink != "" {
		if strings.HasPrefix(symlink, "/usr/local/bin/") && !strings.HasPrefix(entrypoint, "/usr/local/bin/") {
			entrypoint, symlink = symlink, entrypoint
		}
	}

	// If we only have entrypoint and no symlink, derive the standard pair.
	if entrypoint != "" && symlink == "" {
		name := path.Base(entrypoint)
		if strings.HasPrefix(entrypoint, "/usr/local/bin/") {
			symlink = "/usr/bin/" + name
		} else if strings.HasPrefix(entrypoint, "/usr/bin/") {
			symlink = entrypoint
			entrypoint = "/usr/local/bin/" + name
		} else {
			// Root-level or non-standard path (e.g. /dropgz from a scratch stage).
			// Keep the path as-is and add a /usr/bin/ symlink.
			symlink = "/usr/bin/" + name
		}
	}

	if entrypoint == "" {
		return defaultEntrypoints(binaries)
	}

	return entrypoint, symlink
}

// defaultEntrypoints returns standard /usr/local/bin + /usr/bin paths from the first binary name.
func defaultEntrypoints(binaries []contents.SpecBinary) (string, string) {
	name := ""
	if len(binaries) > 0 && binaries[0].Name != "" {
		name = binaries[0].Name
	}
	if name == "" {
		return "", ""
	}
	return "/usr/local/bin/" + name, "/usr/bin/" + name
}

// ─── Chunk 9 · STAGE HELPERS ─────────────────────────────────────────────────

// findBuilderStage returns the index of the primary Go builder stage.
// First pass: prefer a stage that actually contains `go build` RUN commands —
// this correctly handles multi-stage Dockerfiles where a toolchain image
// (e.g. mcr.microsoft.com/oss/go/microsoft/golang) is a separate base stage
// and the real builder stage references it by alias (e.g. FROM go AS builder).
// Second pass (fallback): any stage with a Go SDK base image.
func findBuilderStage(stages []contents.Stage) int {
	for i, stage := range stages {
		for _, run := range stage.Runs {
			if goBuildRe.MatchString(run) {
				return i
			}
		}
	}
	// Fallback: stage with Go SDK base image (may have no direct go build commands).
	for i, stage := range stages {
		if IsGoImage(stage.From) {
			return i
		}
	}
	return -1
}

// findFinalStage returns the index of the final image stage (last non-referenced,
// non-builder, non-windows stage).
func findFinalStage(stages []contents.Stage) int {
	if len(stages) == 0 {
		return -1
	}

	referenced := make(map[string]bool)
	for i, stage := range stages {
		for _, cp := range stage.Copies {
			if cp.From != "" {
				referenced[cp.From] = true
			}
		}
		for j := 0; j < i; j++ {
			if stages[j].Name != "" && stages[j].Name == stage.From {
				referenced[stages[j].Name] = true
			}
		}
	}

	for i := len(stages) - 1; i >= 0; i-- {
		stage := stages[i]
		ref := stage.Name
		if ref == "" {
			ref = fmt.Sprintf("%d", i)
		}
		if referenced[ref] {
			continue
		}
		if IsGoImage(stage.From) {
			continue
		}
		if IsWindowsImage(stage.From) {
			continue
		}
		// Transitively check if the stage's From resolves to a Windows image
		// (e.g. FROM hpc AS windows where hpc itself is a Windows base image alias).
		if resolvedFrom := resolveStageFrom(stage.From, stages); IsWindowsImage(resolvedFrom) {
			continue
		}
		if strings.Contains(strings.ToLower(stage.Platform), "windows") {
			continue
		}
		return i
	}
	return len(stages) - 1
}

// resolveStageFrom follows the stage alias chain and returns the first
// external image reference (not a stage alias). Used to transitively
// detect Windows base images when intermediate aliases hide the origin.
func resolveStageFrom(from string, stages []contents.Stage) string {
	for i := 0; i < len(stages); i++ {
		for _, s := range stages {
			if strings.EqualFold(s.Name, from) {
				from = s.From
				break
			}
		}
	}
	return from
}

// findIntermediateStages returns indices of stages between the builder and
// the final stage that contain pipeline/wrapper steps.
func findIntermediateStages(stages []contents.Stage, builderIdx int) []int {
	finalIdx := findFinalStage(stages)
	builderName := stages[builderIdx].Name

	var indices []int
	for i := builderIdx + 1; i < len(stages); i++ {
		if i == finalIdx {
			continue
		}
		if stages[i].From == builderName || IsGoImage(stages[i].From) || isStageSelfReference(stages[i].From, stages, i) {
			indices = append(indices, i)
		}
	}
	return indices
}
