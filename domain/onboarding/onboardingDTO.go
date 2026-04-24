package onboarding

import "fmt"

// ReviewMode controls how generated specs are delivered.
type ReviewMode string

const (
	ManualReview ReviewMode = "ManualReview" // Generate spec → test → create PR for approval (default)
	AutoReview   ReviewMode = "AutoReview"   // Generate spec → test → push directly to remote (no PR)
)

type OnboardingInfo struct {
	Repository     string      `yaml:"repository"`
	Tag            []string    `yaml:"tags"`
	Targets        []string    `yaml:"targets"`
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

// SpecDir returns the specs directory path for this onboarding entry.
// e.g. "specs/containernetworking/azure-cns" or "specs/azure-cns".
func (o *OnboardingInfo) SpecDir() string {
	if o.SpecRepository != "" {
		return fmt.Sprintf("specs/%s/%s", o.SpecRepository, o.SpecImageName)
	}
	return fmt.Sprintf("specs/%s", o.SpecImageName)
}

// SpecLeaf returns the leaf path segment: "specRepo/specImage" or just "specImage".
func (o *OnboardingInfo) SpecLeaf() string {
	if o.SpecRepository != "" {
		return fmt.Sprintf("%s/%s", o.SpecRepository, o.SpecImageName)
	}
	return o.SpecImageName
}