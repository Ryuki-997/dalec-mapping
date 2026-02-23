package contents

import (
	"dalec-mapping/domain/onboarding"
	"dalec-mapping/domain/repository"
)

// DefaultSpec combines GitHub repo info, parsed Dockerfile info, and Makefile info
type DefaultSpec struct {
	repository.RepoInfo
	DockerfileInfo
	onboarding.OnboardingInfo

	Revision     int
	BuildTargets []BuildTarget
}