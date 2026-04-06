package onboarding

// ReviewMode controls how generated specs are delivered.
type ReviewMode string

const (
	ManualReview ReviewMode = "ManualReview" // Generate spec → test → create PR for approval (default)
	AutoReview   ReviewMode = "AutoReview"   // Generate spec → test → push directly to remote (no PR)
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