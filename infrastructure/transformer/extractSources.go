package transformer

// ═══════════════════════════════════════════════════════════════════════════════
// extractSources.go — Generates the `sources:` section of a Dalec spec.
//
//   Chunk 1 · ORCHESTRATION            extractSourcesSection()
//     Assembles all source entries: primary repo, sub-modules, MinGW toolchain.
//     Calls → buildPrimarySource(), buildSubmoduleSources(), buildMingwSource()
//
//   Chunk 2 · PRIMARY SOURCE           buildPrimarySource()
//     Main git repo + gomod generate entries (root + any subdirectories).
//     Calls → collectGoModSubpaths()
//
//   Chunk 3 · SUBMODULE SOURCES        buildSubmoduleSources()
//     Separate git+gomod entries for each `go mod download` dependency.
//
//   Chunk 4 · MINGW TOOLCHAIN          buildMingwSource()
//     HTTP source for the MinGW cross-compiler (windowscross targets only).
//
//   Chunk 5 · DISCOVERY                DetectGoModDownloads(), collectGoModSubpaths()
//     Scans pipeline steps + binary commands to detect go mod download patterns
//     and literal cd subdirectories that need gomod generate entries.
//
//   Chunk 6 · RESOLUTION               resolveVersionVar(), resolveSubmoduleCommit()
//     Resolves version variables and sub-module tags to git commit SHAs.
// ═══════════════════════════════════════════════════════════════════════════════

import (
	"dalec-mapping/domain/contents"
	"dalec-mapping/domain/llm"
	"dalec-mapping/infrastructure/github"
	"fmt"
	"regexp"
	"strings"
)

// mingwURL is the prebuilt MinGW cross-compiler toolchain for a Linux/x86_64 host.
// It provides x86_64-w64-mingw32-clang for windows/amd64 cross-builds.
// Note: the release only ships ubuntu-20.04 Linux host builds.
const mingwURL = "https://github.com/mstorsjo/llvm-mingw/releases/download/20241217/llvm-mingw-20241217-ucrt-ubuntu-20.04-x86_64.tar.xz"

// MingwSourceKey is the Dalec source name for the MinGW toolchain.
const MingwSourceKey = "mingw-toolchain"

// MingwBinDir is the absolute path to the bin/ directory inside the build sandbox.
// Dalec strips the top-level directory from http source tarballs and extracts the
// contents under /build/top/BUILD/<key>/ during RPM %prep.
const MingwBinDir = "/build/top/BUILD/" + MingwSourceKey + "/bin"

// cdLiteralRe matches the leading `cd <dir> &&` of a build command where <dir> is a
// literal path (no shell variable references like ${X}).  Used to discover extra
// Go-module subdirectories that need their own gomod generate entry.
var cdLiteralRe = regexp.MustCompile(`^cd\s+([^\s${}]+)\s*&&`)

// goModDownloadRe matches `go mod download <module>@<version>` in pipeline steps.
// Captures: (1) full module path, (2) version.
// Example: go mod download github.com/azure/azure-container-networking/dropgz@${DROPGZ_VERSION}
var goModDownloadRe = regexp.MustCompile(`go\s+mod\s+download\s+(\S+)@(\S+)`)

// GoModDownloadInfo holds the parsed info from a `go mod download` pipeline step.
type GoModDownloadInfo struct {
	// SourceKey is the short name used as the DALEC source key (e.g. "dropgz").
	SourceKey string
	// ModulePath is the full Go module path (e.g. "github.com/azure/azure-container-networking/dropgz").
	ModulePath string
	// SubPath is the subdirectory within the git repo (e.g. "dropgz").
	SubPath string
	// VersionVar is the version reference (e.g. "${DROPGZ_VERSION}" or "v0.0.12").
	VersionVar string
	// CommitArgName is the arg name for the resolved commit SHA (e.g. "DROPGZ_COMMIT").
	CommitArgName string
	// CommitSHA is the resolved commit SHA (empty if resolution failed).
	CommitSHA string
	// GitURL is the repository URL (e.g. "https://github.com/azure/azure-container-networking").
	GitURL string
}

// ─── Chunk 1 · ORCHESTRATION ─────────────────────────────────────────────────

// extractSourcesSection assembles all source entries for the Dalec spec.
// Pre-computed goModDownloads are emitted as separate prefetched sources.
func extractSourcesSection(defaultSpec *contents.DefaultSpec, nonDeterministicValues *llm.NonDeterministicValues, goModDownloads []GoModDownloadInfo) map[string]interface{} {
	sources := make(map[string]interface{})

	buildPrimarySource(sources, defaultSpec, nonDeterministicValues)
	buildSubmoduleSources(sources, defaultSpec, goModDownloads)
	buildMingwSource(sources, defaultSpec)

	return sources
}

// ─── Chunk 2 · PRIMARY SOURCE ────────────────────────────────────────────────

// buildPrimarySource adds the main repo git source with gomod generate entries
// for the root module and any subdirectories discovered in build commands.
func buildPrimarySource(sources map[string]interface{}, defaultSpec *contents.DefaultSpec, nonDeterministicValues *llm.NonDeterministicValues) {
	subpaths := collectGoModSubpaths(defaultSpec, nonDeterministicValues)

	generateEntries := []map[string]interface{}{
		{string(defaultSpec.Generator): map[string]interface{}{}},
	}
	for _, sub := range subpaths {
		generateEntries = append(generateEntries, map[string]interface{}{
			string(defaultSpec.Generator): map[string]interface{}{},
			"subpath":                     sub,
		})
	}

	sources[defaultSpec.Repo] = map[string]interface{}{
		"git": map[string]interface{}{
			"url":    defaultSpec.GitURL,
			"commit": "${COMMIT}",
		},
		"generate": generateEntries,
	}
}

// ─── Chunk 3 · SUBMODULE SOURCES ─────────────────────────────────────────────

// buildSubmoduleSources adds a separate git+gomod entry for each pre-computed
// go mod download dependency (e.g. dropgz).
func buildSubmoduleSources(sources map[string]interface{}, defaultSpec *contents.DefaultSpec, goModDownloads []GoModDownloadInfo) {
	for _, info := range goModDownloads {
		entry := map[string]interface{}{
			"git": map[string]interface{}{
				"url":    info.GitURL,
				"commit": fmt.Sprintf("${%s}", info.CommitArgName),
			},
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

// ─── Chunk 4 · MINGW TOOLCHAIN ──────────────────────────────────────────────

// buildMingwSource adds the MinGW cross-compiler HTTP source when any build
// target requires windowscross.
func buildMingwSource(sources map[string]interface{}, defaultSpec *contents.DefaultSpec) {
	for _, bt := range defaultSpec.BuildTargets {
		if strings.HasPrefix(string(bt), "windowscross") {
			sources[MingwSourceKey] = map[string]interface{}{
				"http": map[string]interface{}{
					"url": mingwURL,
				},
			}
			return
		}
	}
}

// ─── Chunk 5 · DISCOVERY ─────────────────────────────────────────────────────

// collectGoModSubpaths returns ordered unique subdirectory paths found via
// literal `cd <subdir> &&` in binary build commands and pipeline steps.
func collectGoModSubpaths(defaultSpec *contents.DefaultSpec, ndv *llm.NonDeterministicValues) []string {
	if ndv == nil {
		return nil
	}
	seen := map[string]bool{}
	var subpaths []string

	add := func(s string) {
		s = strings.TrimPrefix(s, "/")
		if s != "" && s != defaultSpec.Repo && !seen[s] {
			seen[s] = true
			subpaths = append(subpaths, s)
		}
	}

	for _, bin := range ndv.Binaries {
		if m := cdLiteralRe.FindStringSubmatch(strings.TrimSpace(bin.BuildCommand)); m != nil {
			add(m[1])
		}
	}
	for _, step := range ndv.PipelineSteps {
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
// `go mod download <module>@<version>` and returns parsed info for each,
// including the resolved commit SHA.
func DetectGoModDownloads(defaultSpec *contents.DefaultSpec, nonDeterministicValues *llm.NonDeterministicValues) []GoModDownloadInfo {
	if nonDeterministicValues == nil {
		return nil
	}

	var stepsToScan []string
	stepsToScan = append(stepsToScan, nonDeterministicValues.PipelineSteps...)
	for _, bin := range nonDeterministicValues.Binaries {
		if bin.BuildCommand != "" {
			stepsToScan = append(stepsToScan, bin.BuildCommand)
		}
	}

	seen := map[string]bool{}
	var results []GoModDownloadInfo
	for _, step := range stepsToScan {
		m := goModDownloadRe.FindStringSubmatch(step)
		if m == nil {
			continue
		}
		modulePath := m[1]
		versionVar := m[2]

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

		resolvedVersion := resolveVersionVar(versionVar, defaultSpec, modulePath)
		commitSHA := resolveSubmoduleCommit(resolvedVersion, parts, sourceKey, defaultSpec)

		results = append(results, GoModDownloadInfo{
			SourceKey:     sourceKey,
			ModulePath:    modulePath,
			SubPath:       subPath,
			VersionVar:    versionVar,
			CommitArgName: strings.ToUpper(sourceKey) + "_COMMIT",
			CommitSHA:     commitSHA,
			GitURL:        gitURL,
		})
		fmt.Printf("📦 Detected sub-module source: %s (commit: %s)\n", sourceKey, commitSHA)
	}
	return results
}

// ─── Chunk 6 · RESOLUTION ────────────────────────────────────────────────────

// resolveVersionVar resolves a version variable reference to a literal string.
func resolveVersionVar(versionVar string, defaultSpec *contents.DefaultSpec, modulePath string) string {
	if strings.HasPrefix(versionVar, "${") || strings.HasPrefix(versionVar, "$(") {
		varName := strings.Trim(versionVar, "${()}")
		if v, ok := defaultSpec.Args[varName]; ok && v != "" {
			return v
		}
		fmt.Printf("⚠️  Cannot resolve %s from Dockerfile args for go mod download %s\n", versionVar, modulePath)
		return ""
	}
	return versionVar
}

// resolveSubmoduleCommit resolves a sub-module version to a git commit SHA.
// Falls back to the main repo's commit if tag resolution fails.
func resolveSubmoduleCommit(resolvedVersion string, moduleParts []string, sourceKey string, defaultSpec *contents.DefaultSpec) string {
	if resolvedVersion != "" && len(moduleParts) >= 3 {
		owner := moduleParts[1]
		repo := moduleParts[2]
		tagRef := sourceKey + "/" + resolvedVersion
		sha, err := github.FetchTagCommit(owner, repo, tagRef)
		if err != nil {
			sha, err = github.FetchTagCommit(owner, repo, resolvedVersion)
		}
		if err == nil {
			return sha
		}
		fmt.Printf("⚠️  Cannot resolve commit for tag %s: %v\n", tagRef, err)
	}

	fmt.Printf("ℹ️  Using main repo commit %s for sub-module %s\n", defaultSpec.LatestCommit, sourceKey)
	return defaultSpec.LatestCommit
}
