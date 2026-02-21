package onboarding

type OnboardingInfo struct {
	Repository  string   `yaml:"repository"`
	Tag         []string   `yaml:"tags"`
	Dockerfile string `yaml:"dockerfile"`
	Makefile   string `yaml:"makefile"`
	SpecImageName string `yaml:"specImageName,omitempty"`
	SpecRepository string `yaml:"specRepository,omitempty"`
}