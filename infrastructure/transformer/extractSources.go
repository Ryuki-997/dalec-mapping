package transformer

import (
	"dalec-mapping/domain/contents"
	"dalec-mapping/domain/llm"
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

// It always emits a single git source with a go-module generator.
// When the repo URL includes a subdirectory, a `subpath` field is added to the
// generator entry so Dalec runs the module fetch from the correct sub-tree.
// When nonDeterministicValues contains binaries whose buildCommand starts with a
// literal `cd <subdir> &&` (i.e. the subdir is not a shell variable), an additional
// gomod generate entry is emitted for that subpath.  This handles monorepos where the
// packaging binary (e.g. `dropgz`) lives in a different Go module from the primary
// binary (e.g. `azure-ipam`).
func extractSources(defaultSpec *contents.DefaultSpec, nonDeterministicValues *llm.NonDeterministicValues) map[string]interface{} {
	// Collect ordered unique gomod subpaths.
	seen := map[string]bool{}
	var subpaths []string

	addSubpath := func(s string) {
		if s != "" && !seen[s] {
			seen[s] = true
			subpaths = append(subpaths, s)
		}
	}

	// Primary subpath (from onboarding / repo URL subdir).
	addSubpath(defaultSpec.Subdir)

	// Additional subpaths from literal `cd <subdir> &&` in binary buildCommands.
	if nonDeterministicValues != nil {
		for _, bin := range nonDeterministicValues.Binaries {
			if m := cdLiteralRe.FindStringSubmatch(strings.TrimSpace(bin.BuildCommand)); m != nil {
				addSubpath(m[1])
			}
		}
	}

	// Build the generate entries slice.
	var generateEntries []map[string]interface{}
	if len(subpaths) == 0 {
		generateEntries = []map[string]interface{}{
			{string(defaultSpec.Generator): map[string]interface{}{}},
		}
	} else {
		for _, sub := range subpaths {
			generateEntries = append(generateEntries, map[string]interface{}{
				string(defaultSpec.Generator): map[string]interface{}{},
				"subpath":                     sub,
			})
		}
	}

	sources := map[string]interface{}{
		defaultSpec.Repo: map[string]interface{}{
			"git": map[string]interface{}{
				"url":    defaultSpec.GitURL,
				"commit": "${COMMIT}",
			},
			"generate": generateEntries,
		},
	}

	// When windowscross/container is a build target, add the MinGW toolchain
	// as an http source for windows/amd64 cross-builds.
	for _, bt := range defaultSpec.BuildTargets {
		if strings.HasPrefix(string(bt), "windowscross") {
			sources[MingwSourceKey] = map[string]interface{}{
				"http": map[string]interface{}{
					"url": mingwURL,
				},
			}
			break
		}
	}

	return sources
}
