package onboarding

// OnboardMode indicates whether this is a first-time onboard, a revision bump,
// or a mismatch that should halt processing.
type OnboardMode int

const (
	ManualReview OnboardMode = iota // First time or Siblings exist but content differs — requires manual review
	CommitBump                      // Alias: both files match, commit-level bump
)

type OnboardingInfo struct {
	Repository     string      `yaml:"repository"`
	Tag            []string    `yaml:"tags"`
	Reviewers 		[]string    `yaml:"reviewers,omitempty"`
	Mode           OnboardMode `yaml:"-"` // set during onboard file discovery
	DockerfileDir     string      `yaml:"dockerfile"`
	MakefileDir       string      `yaml:"makefile"`
	OnboardDir     string      `yaml:"-"` // folder path in onboard repo (e.g. "specs/containernetworking/azure-cns")
	SpecImageName  string      `yaml:"specImageName,omitempty"`
	SpecRepository string      `yaml:"specRepository,omitempty"`

	// Dockerfile/Makefile content (cached from onboard repo in step1, then overwritten with fresh content in step2).
	DockerfileContent []byte `yaml:"-"`
	MakefileContent   []byte `yaml:"-"`
}