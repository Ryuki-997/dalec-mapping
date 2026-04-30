package transformer

// ═══════════════════════════════════════════════════════════════════════════════
// extractSources.go — Generates the `sources:` section of a Dalec spec.
//
//   Chunk 1 · ORCHESTRATION            extractSourcesSection()
//     Assembles all source entries: primary repo, sub-modules.
//     Calls → buildPrimarySource(), buildSubmoduleSources()
//
//   Chunk 2 · PRIMARY SOURCE           buildPrimarySource()
//     Main git repo + gomod generate entries (root + any subdirectories).
//     Calls → collectGoModSubpaths()
//
//   Chunk 3 · SUBMODULE SOURCES        buildSubmoduleSources()
//     Separate git+gomod entries for each `go mod download` dependency.
//
//   Chunk 4 · DISCOVERY                DetectGoModDownloads(), collectGoModSubpaths()
//     Scans pipeline steps + binary commands to detect go mod download patterns
//     and literal cd subdirectories that need gomod generate entries.
// ═══════════════════════════════════════════════════════════════════════════════

import (
	"dalec-mapping/domain/contents"
	"dalec-mapping/infrastructure/repository"
	"fmt"
	"log"
	"regexp"
	"strings"
)

// cdLiteralRe matches the leading `cd <dir> &&` or `cd <dir>\n` of a build command
// where <dir> is a literal path (no shell variable references like ${X}).
// Used to discover extra Go-module subdirectories that need their own gomod generate entry.
var cdLiteralRe = regexp.MustCompile(`(?s)^cd\s+([^\s${}]+)\s*(?:&&|\n)`)

// goModDownloadRe matches `go mod download <module>@<version>` in pipeline steps.
// Captures: (1) full module path, (2) version.
// Example: go mod download github.com/azure/azure-container-networking/dropgz@${DROPGZ_VERSION}
var goModDownloadRe = regexp.MustCompile(`go\s+mod\s+download\s+(\S+)@(\S+)`)

// goModCdRe matches `cd /go/pkg/mod/<module>@<version>` — used as a fallback
// to detect submodules when `go mod download` is absent (handled as a DALEC source).
// Captures: (1) full module path, (2) version.
var goModCdRe = regexp.MustCompile(`cd\s+/go/pkg/mod/(\S+)@(\S+)`)

// GoModDownloadInfo holds the parsed info from a `go mod download` pipeline step.
type GoModDownloadInfo struct {
	// SourceKey is the short name used as the DALEC source key (e.g. "dropgz").
	SourceKey string
	// ModulePath is the full Go module path (e.g. "github.com/azure/azure-container-networking/dropgz").
	ModulePath string
	// SubPath is the subdirectory within the git repo (e.g. "dropgz").
	SubPath string
	// VersionVar is the version reference as it appears in the Dockerfile (e.g. "${DROPGZ_VERSION}").
	VersionVar string
	// GitURL is the repository URL (e.g. "https://github.com/azure/azure-container-networking").
	GitURL string
}

// ─── Chunk 1 · ORCHESTRATION ─────────────────────────────────────────────────

// extractSourcesSection assembles all source entries for the Dalec spec.
// Pre-computed goModDownloads are emitted as separate prefetched sources.
func extractSourcesSection(defaultSpec *contents.DefaultSpec, goModDownloads []GoModDownloadInfo) map[string]interface{} {
	sources := make(map[string]interface{})

	buildPrimarySource(sources, defaultSpec)
	buildSubmoduleSources(sources, defaultSpec, goModDownloads)

	return sources
}

// ─── Chunk 2 · PRIMARY SOURCE ────────────────────────────────────────────────

// buildPrimarySource adds the main repo git source with gomod generate entries
// for the root module and any subdirectories discovered in build commands.
// When a ComponentPath is set on the spec, it is used as the primary subpath
// and gomod discovery is scoped within it. Otherwise falls back to heuristics
// from MakefileDir/DockerfileDir.
func buildPrimarySource(sources map[string]interface{}, defaultSpec *contents.DefaultSpec) {
	subpaths := collectGoModSubpaths(defaultSpec, contents.Spec)

	// Determine where the primary go.mod lives.
	// Priority: ComponentPath > MakefileDir > gomod subpath > DockerfileDir
	rootGoModSubpath := ""
	if defaultSpec.ComponentPath != "" {
		rootGoModSubpath = defaultSpec.ComponentPath
	}
	if rootGoModSubpath == "" && defaultSpec.MakefileDir != "" {
		dir := strings.TrimSuffix(defaultSpec.MakefileDir, "/")
		if idx := strings.LastIndex(dir, "/"); idx >= 0 {
			rootGoModSubpath = dir[:idx]
		}
	}
	if rootGoModSubpath == "" && len(subpaths) > 0 {
		rootGoModSubpath = subpaths[0]
	}
	if rootGoModSubpath == "" && defaultSpec.DockerfileDir != "" {
		d := strings.TrimSuffix(defaultSpec.DockerfileDir, "/")
		if idx := strings.LastIndex(d, "/"); idx >= 0 {
			rootGoModSubpath = d[:idx]
		}
	}

	rootEntry := map[string]interface{}{
		string(defaultSpec.Generator): map[string]interface{}{},
	}
	if rootGoModSubpath != "" {
		rootEntry["subpath"] = rootGoModSubpath
	}

	generateEntries := []map[string]interface{}{rootEntry}
	for _, sub := range subpaths {
		generateEntries = append(generateEntries, map[string]interface{}{
			string(defaultSpec.Generator): map[string]interface{}{},
			"subpath":                     sub,
		})
	}

	gitBlock := map[string]interface{}{
		"url":    defaultSpec.GitURL,
		"commit": "${COMMIT}",
	}
	if repository.IsADORepo(defaultSpec.GitURL) {
		gitBlock["auth"] = map[string]interface{}{
			"header": "GIT_AUTH_HEADER",
		}
	}

	sources[defaultSpec.Repo] = map[string]interface{}{
		"git":      gitBlock,
		"generate": generateEntries,
	}
}

// ─── Chunk 3 · SUBMODULE SOURCES ─────────────────────────────────────────────

// buildSubmoduleSources adds a separate git+gomod entry for each pre-computed
// go mod download dependency (e.g. dropgz).
func buildSubmoduleSources(sources map[string]interface{}, defaultSpec *contents.DefaultSpec, goModDownloads []GoModDownloadInfo) {
	for _, info := range goModDownloads {
		// Format commit as <subPath>/<versionVar> to match Go module tag convention
		// e.g. "dropgz/${DROPGZ_VERSION}" resolves to git tag "dropgz/v0.0.12"
		commitRef := info.VersionVar
		if info.SubPath != "" {
			commitRef = info.SubPath + "/" + info.VersionVar
		}
		subGitBlock := map[string]interface{}{
			"url":    info.GitURL,
			"commit": commitRef,
		}
		if repository.IsADORepo(info.GitURL) {
			subGitBlock["auth"] = map[string]interface{}{
				"header": "GIT_AUTH_HEADER",
			}
		}

		entry := map[string]interface{}{
			"git": subGitBlock,
			"generate": []map[string]interface{}{
				{string(defaultSpec.Generator): map[string]interface{}{}},
			},
		}
		if info.SubPath != "" {
			entry["path"] = info.SubPath
		}
		sources[info.SourceKey] = entry
	}
}

// ─── Chunk 4 · DISCOVERY ─────────────────────────────────────────────────────

// collectGoModSubpaths returns ordered unique subdirectory paths found via
// literal `cd <subdir> &&` in binary build commands and pipeline steps.
// When a ComponentPath is set on defaultSpec, only subpaths that fall within
// the component directory (DFS) are included.
func collectGoModSubpaths(defaultSpec *contents.DefaultSpec, spec *contents.DockerfileSpec) []string {
	if spec == nil {
		return nil
	}
	seen := map[string]bool{}
	var subpaths []string
	componentPath := defaultSpec.ComponentPath

	add := func(s string) {
		s = strings.TrimPrefix(s, "/")
		if s == "" || s == defaultSpec.Repo || seen[s] {
			return
		}
		// When a component path is set, only accept subpaths within it.
		if componentPath != "" && !strings.HasPrefix(s, componentPath+"/") && s != componentPath {
			return
		}
		seen[s] = true
		subpaths = append(subpaths, s)
	}

	for _, bin := range spec.Binaries {
		if m := cdLiteralRe.FindStringSubmatch(strings.TrimSpace(bin.BuildCommand)); m != nil {
			add(m[1])
		}
	}
	for _, step := range spec.PipelineSteps {
		for _, line := range strings.Split(step, "\n") {
			if m := cdLiteralRe.FindStringSubmatch(strings.TrimSpace(line)); m != nil {
				if !strings.HasPrefix(m[1], "/") {
					add(m[1])
				}
			}
		}
	}
	return subpaths
}

// DetectGoModDownloads scans pipeline steps AND binary build commands for
// submodule references. Detects both `go mod download <module>@<version>`
// (legacy) and `cd /go/pkg/mod/<module>@<version>` patterns and returns
// parsed info for each.
func DetectGoModDownloads(defaultSpec *contents.DefaultSpec) []GoModDownloadInfo {
	if contents.Spec == nil {
		return nil
	}

	var stepsToScan []string
	stepsToScan = append(stepsToScan, contents.Spec.PipelineSteps...)
	for _, bin := range contents.Spec.Binaries {
		if bin.BuildCommand != "" {
			stepsToScan = append(stepsToScan, bin.BuildCommand)
		}
	}

	seen := map[string]bool{}
	var results []GoModDownloadInfo
	for _, step := range stepsToScan {
		// Try `go mod download <module>@<version>` first (legacy),
		// then fall back to `cd /go/pkg/mod/<module>@<version>`.
		m := goModDownloadRe.FindStringSubmatch(step)
		if m == nil {
			m = goModCdRe.FindStringSubmatch(step)
		}
		if m == nil {
			continue
		}
		modulePath := m[1]
		versionVar := m[2]

		// Normalize to ${VAR} delimiter — DALEC uses this syntax to verify and
		// resolve variable references in commit fields.
		bare := strings.TrimLeft(versionVar, "$")
		bare = strings.Trim(bare, "{}()")
		versionVar = "${" + bare + "}"

		parts := strings.Split(modulePath, "/")
		sourceKey := parts[len(parts)-1]

		if seen[sourceKey] {
			continue
		}
		seen[sourceKey] = true

		gitURL := defaultSpec.GitURL
		subPath := sourceKey
		if len(parts) >= 3 {
			gitURL = fmt.Sprintf("https://%s/%s/%s", parts[0], parts[1], parts[2])
			if len(parts) > 3 {
				subPath = strings.Join(parts[3:], "/")
			}
		}

		results = append(results, GoModDownloadInfo{
			SourceKey:  sourceKey,
			ModulePath: modulePath,
			SubPath:    subPath,
			VersionVar: versionVar,
			GitURL:     gitURL,
		})
		log.Printf("📦 Detected sub-module source: %s (version: %s)\n", sourceKey, versionVar)
	}
	return results
}
