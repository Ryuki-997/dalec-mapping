package transformer

import (
	"dalec-mapping/domain/contents"
	"strings"
)

// mingwToolchainURL is the prebuilt Windows-ARM64 MinGW cross-compiler from
// Windows-on-ARM-Experiments/mingw-woarm64-build. Targeting aarch64-w64-mingw32.
const mingwToolchainURL = "https://github.com/Windows-on-ARM-Experiments/mingw-woarm64-build/releases/download/2025-07-15/aarch64-w64-mingw32-msvcrt-toolchain.tar.gz"

// MingwToolchainSourceKey is the Dalec source name for the MinGW toolchain.
// The toolchain is mounted at /<MingwToolchainSourceKey>/ in the build sandbox.
const MingwToolchainSourceKey = "aarch64-mingw-toolchain"

// MingwGCCPath is the absolute path to aarch64-w64-mingw32-gcc inside the build sandbox.
// Dalec copies http sources into /build/top/BUILD/ during %prep, so the toolchain
// lands at /build/top/BUILD/<key>/. The %build section runs from that same directory.
const MingwGCCPath = "/build/top/BUILD/" + MingwToolchainSourceKey + "/bin/aarch64-w64-mingw32-gcc"

// extractSources builds the `sources:` map for the Dalec spec.
// It always emits a single git source with a go-module generator.
// When the repo URL includes a subdirectory, a `subpath` field is added to the
// generator entry so Dalec runs the module fetch from the correct sub-tree.
// When windowscross/container is a build target, the prebuilt Windows-ARM64
// MinGW toolchain is added as an http source.
func extractSources(defaultSpec *contents.DefaultSpec) map[string]interface{} {
	// Generator entry: { gomod: {} } plus optional subpath for monorepo subdirs.
	generatorEntry := map[string]interface{}{
		string(defaultSpec.Generator): map[string]interface{}{},
	}
	if defaultSpec.Subdir != "" {
		generatorEntry["subpath"] = defaultSpec.Subdir
	}

	sources := map[string]interface{}{
		defaultSpec.Repo: map[string]interface{}{
			"git": map[string]interface{}{
				"url":    defaultSpec.GitURL,
				"commit": "${COMMIT}",
			},
			"generate": []map[string]interface{}{generatorEntry},
		},
	}

	for _, bt := range defaultSpec.BuildTargets {
		if strings.HasPrefix(string(bt), "windowscross") {
			sources[MingwToolchainSourceKey] = map[string]interface{}{
				"http": map[string]interface{}{
					"url": mingwToolchainURL,
				},
			}
			break
		}
	}

	return sources
}
