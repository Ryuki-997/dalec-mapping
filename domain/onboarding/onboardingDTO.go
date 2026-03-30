package onboarding

// ReviewMode indicates whether this is a first-time onboard, a revision bump,
// or a content change that requires owner notification.
type ReviewMode string

const (
	ManualReview   ReviewMode = "ManualReview"  // Both Initial and Content changes require manual review (default) 
	AutoReview     ReviewMode = "AutoReview"    // Auto generates
)

type OnboardingInfo struct {
	Repository     string      `yaml:"repository"`
	Tag            []string    `yaml:"tags"`
	Reviewers      []string    `yaml:"reviewers,omitempty"`
	ReviewMode     ReviewMode  `yaml:"reviewMode,omitempty"` 
	DockerfileDir     string      `yaml:"dockerfile"`
	MakefileDir       string      `yaml:"makefile"`
	OnboardDir     string      `yaml:"-"` // folder path in onboard repo (e.g. "specs/containernetworking/azure-cns")
	SpecImageName  string      `yaml:"specImageName,omitempty"`
	SpecRepository string      `yaml:"specRepository,omitempty"`

	// Dockerfile/Makefile content (cached from onboard repo in step1, then overwritten with fresh content in step2).
	DockerfileContent []byte `yaml:"-"`
	MakefileContent   []byte `yaml:"-"`
}