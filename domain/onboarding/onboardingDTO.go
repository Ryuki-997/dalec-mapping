package onboarding

type OnboardingInfo struct {
	Repository  string   `yaml:"repository"`
	Tag         []string   `yaml:"tags"`
	Dockerfile []string `yaml:"dockerfiles"`
	Makefile   []string `yaml:"makefiles"`
}