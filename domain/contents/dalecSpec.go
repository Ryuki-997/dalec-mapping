package contents

import (
	"dalec-mapping/domain/onboarding"
	"dalec-mapping/domain/repository"
)

// DefaultSpec combines GitHub repo info, parsed Dockerfile info, and Makefile info.
type DefaultSpec struct {
	repository.RepoInfo
	DockerfileInfo
	onboarding.ComponentConfig

	Revision     int
	BuildTargets []BuildTarget
	GoVersion    string // Go toolchain version extracted from Dockerfile (e.g. "1.24")
}

// PreviousDalecSpec represents the args section of a previously generated spec file.
type PreviousDalecSpec struct {
	Args struct {
		Version  string `yaml:"VERSION"`
		Revision int    `yaml:"REVISION"`
	} `yaml:"args"`
}
