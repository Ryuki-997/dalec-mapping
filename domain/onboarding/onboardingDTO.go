package onboarding

// OnboardMode indicates whether this is a first-time onboard, a revision bump,
// or a content change that requires owner notification.
type OnboardMode int

const (
	FirstOnboard   OnboardMode = iota // No cached siblings — first-time onboard, requires review
	ContentChanged                    // Siblings exist but Dockerfile/Makefile content differs
	CommitBump                        // Both files match — commit-level bump only
)

type OnboardingInfo struct {
	Repository     string      `yaml:"repository"`
	Tag            []string    `yaml:"tags"`
	Reviewers      []string    `yaml:"reviewers,omitempty"`
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