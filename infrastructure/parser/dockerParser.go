package parser

import (
	"dalec-mapping/domain/contents"
	"fmt"
	"os"
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
	fmt.Println("Parsed Dockerfile Stages:")
	fmt.Println("")

	for _, stage := range defaultSpec.Stages {
		fmt.Printf("Stage: %s (From: %s)\n", stage.Name, stage.From)
		fmt.Println("  Env:")
		for k, v := range stage.Env {
			fmt.Printf("    %s=%s\n", k, v)
		}
		fmt.Println("  Runs:")
		for _, run := range stage.Runs {
			fmt.Printf("    %s\n", run)
		}
		fmt.Println("  Copies:")
		for _, copy := range stage.Copies {
			fmt.Printf("    From: %s, Source: %v, Dest: %s\n", copy.From, copy.Source, copy.Dest)
		}
		fmt.Println("")
	}

	for k, v := range defaultSpec.Args {
		fmt.Printf("Build Arg: %s=%s\n", k, v)
	}
	fmt.Println("")
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

// ═══════════════════════════════════════════════════════════════════════════════
// DOCKERFILE ANALYSIS — Analyses multi-stage Dockerfiles for patterns
// that map to Dalec spec fields but are NOT handled by the LLM.
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

		fmt.Printf("📦 Intermediate stage %q installs packages: %v (selective=%v)\n",
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
