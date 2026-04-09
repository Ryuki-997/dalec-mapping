package contents

import (
	"dalec-mapping/domain/onboarding"
	"dalec-mapping/domain/repository"
)

// ─── Global source of truth ───────────────────────────────────────────────────
//
// These package-level variables are set once during the parse phase and read
// throughout the transformer pipeline. No function needs to accept or return
// DockerfileInfo, MakefileInfo, or DockerfileSpec as parameters.
var (
	// Dockerfile holds the raw AST result of parsing the project's Dockerfile.
	Dockerfile DockerfileInfo

	// Makefile holds variables extracted from the project's Makefile.
	Makefile MakefileInfo

	// Spec holds the static build values derived from Dockerfile AST analysis.
	Spec *DockerfileSpec
)

// DefaultSpec combines GitHub repo info, parsed Dockerfile info, and Makefile info.
type DefaultSpec struct {
	repository.RepoInfo
	DockerfileInfo
	onboarding.OnboardingInfo

	Revision     int
	BuildTargets []BuildTarget
	GoVersion    string // Go toolchain version extracted from Dockerfile (e.g. "1.24")
}